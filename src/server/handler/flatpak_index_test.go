// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	beegocontext "github.com/beego/beego/v2/server/web/context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/goharbor/harbor/src/controller/artifact"
	ctltag "github.com/goharbor/harbor/src/controller/tag"
	"github.com/goharbor/harbor/src/lib/errors"
	pkgart "github.com/goharbor/harbor/src/pkg/artifact"
	"github.com/goharbor/harbor/src/pkg/flatpak"
	"github.com/goharbor/harbor/src/pkg/project/models"
	repomodel "github.com/goharbor/harbor/src/pkg/repository/model"
	tagmodel "github.com/goharbor/harbor/src/pkg/tag/model/tag"
	"github.com/goharbor/harbor/src/server/router"
	arttest "github.com/goharbor/harbor/src/testing/controller/artifact"
	protest "github.com/goharbor/harbor/src/testing/controller/project"
	repotest "github.com/goharbor/harbor/src/testing/controller/repository"
)

type FlatpakHandlerTestSuite struct {
	suite.Suite
	handler *flatpakIndexHandler
	proCtl  *protest.Controller
	artCtl  *arttest.Controller
	repoCtl *repotest.Controller
}

func (s *FlatpakHandlerTestSuite) SetupTest() {
	s.proCtl = &protest.Controller{}
	s.artCtl = &arttest.Controller{}
	s.repoCtl = &repotest.Controller{}
	s.handler = &flatpakIndexHandler{
		builder: flatpak.NewBuilder(s.proCtl, s.artCtl, s.repoCtl),
	}
}

func (s *FlatpakHandlerTestSuite) newRequest(method, path, projectName string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	input := beegocontext.NewInput()
	input.SetParam(":splat", projectName)
	ctx := context.WithValue(req.Context(), router.ContextKeyInput{}, input)
	return req.WithContext(ctx)
}

func (s *FlatpakHandlerTestSuite) TestNotFoundWhenProjectDoesNotExist() {
	s.proCtl.On("GetByName", mock.Anything, "nonexistent", mock.Anything).
		Return(nil, errors.NotFoundError(errors.New("project not found")))

	req := s.newRequest(http.MethodGet, "/nonexistent/index/static", "nonexistent")
	w := httptest.NewRecorder()
	s.handler.ServeStatic(w, req)

	s.Equal(http.StatusNotFound, w.Code)
}

func (s *FlatpakHandlerTestSuite) TestNotFoundWhenFlatpakDisabled() {
	pro := &models.Project{
		ProjectID: 1,
		Name:      "myproject",
		Metadata: map[string]string{
			"flatpak_index_enabled": "false",
		},
	}
	s.proCtl.On("GetByName", mock.Anything, "myproject", mock.Anything).Return(pro, nil)

	req := s.newRequest(http.MethodGet, "/myproject/index/static", "myproject")
	w := httptest.NewRecorder()
	s.handler.ServeStatic(w, req)

	s.Equal(http.StatusNotFound, w.Code)
}

func (s *FlatpakHandlerTestSuite) TestOKWithCorrectJSON() {
	pro := &models.Project{
		ProjectID: 1,
		Name:      "myproject",
		Metadata: map[string]string{
			"flatpak_index_enabled": "true",
		},
	}
	s.proCtl.On("GetByName", mock.Anything, "myproject", mock.Anything).Return(pro, nil)
	s.repoCtl.On("List", mock.Anything, mock.Anything).Return([]*repomodel.RepoRecord{
		{RepositoryID: 1, Name: "myproject/app"},
	}, nil)

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:abc123",
				Size:              50000000,
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/x86_64/stable",
				},
				ExtraAttrs: map[string]any{
					"architecture": "amd64",
					"os":           "linux",
				},
			},
			Tags: []*ctltag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	req := s.newRequest(http.MethodGet, "/myproject/index/static", "myproject")
	w := httptest.NewRecorder()
	s.handler.ServeStatic(w, req)

	s.Equal(http.StatusOK, w.Code)
	s.Contains(w.Header().Get("Content-Type"), "application/json")

	var index flatpak.Index
	err := json.Unmarshal(w.Body.Bytes(), &index)
	s.NoError(err)
	s.Len(index.Results, 1)
	s.Equal("myproject/app", index.Results[0].Name)
	s.Len(index.Results[0].Images, 1)
	s.Equal("sha256:abc123", index.Results[0].Images[0].Digest)
	s.Equal("amd64", index.Results[0].Images[0].Architecture)
}

func (s *FlatpakHandlerTestSuite) TestStaticCacheControlHeader() {
	pro := &models.Project{
		ProjectID: 1,
		Name:      "myproject",
		Metadata:  map[string]string{"flatpak_index_enabled": "true"},
	}
	s.proCtl.On("GetByName", mock.Anything, "myproject", mock.Anything).Return(pro, nil)
	s.repoCtl.On("List", mock.Anything, mock.Anything).Return([]*repomodel.RepoRecord{}, nil)

	req := s.newRequest(http.MethodGet, "/myproject/index/static", "myproject")
	w := httptest.NewRecorder()
	s.handler.ServeStatic(w, req)

	s.Equal(http.StatusOK, w.Code)
	s.Contains(w.Header().Get("Cache-Control"), "max-age=")
}

func (s *FlatpakHandlerTestSuite) TestDynamicCacheControlHeader() {
	pro := &models.Project{
		ProjectID: 1,
		Name:      "myproject",
		Metadata:  map[string]string{"flatpak_index_enabled": "true"},
	}
	s.proCtl.On("GetByName", mock.Anything, "myproject", mock.Anything).Return(pro, nil)
	s.repoCtl.On("List", mock.Anything, mock.Anything).Return([]*repomodel.RepoRecord{}, nil)

	req := s.newRequest(http.MethodGet, "/myproject/index/dynamic", "myproject")
	w := httptest.NewRecorder()
	s.handler.ServeDynamic(w, req)

	s.Equal(http.StatusOK, w.Code)
	s.Equal("no-store", w.Header().Get("Cache-Control"))
}

func (s *FlatpakHandlerTestSuite) TestArchitectureQueryParamFilter() {
	pro := &models.Project{
		ProjectID: 1,
		Name:      "myproject",
		Metadata:  map[string]string{"flatpak_index_enabled": "true"},
	}
	s.proCtl.On("GetByName", mock.Anything, "myproject", mock.Anything).Return(pro, nil)
	s.repoCtl.On("List", mock.Anything, mock.Anything).Return([]*repomodel.RepoRecord{
		{RepositoryID: 1, Name: "myproject/app"},
	}, nil)

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:x86",
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/x86_64/stable",
				},
				ExtraAttrs: map[string]any{"architecture": "amd64"},
			},
			Tags: []*ctltag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
		{
			Artifact: pkgart.Artifact{
				ID:                2,
				Digest:            "sha256:arm",
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/aarch64/stable",
				},
				ExtraAttrs: map[string]any{"architecture": "arm64"},
			},
			Tags: []*ctltag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	req := s.newRequest(http.MethodGet, "/myproject/index/static?architecture=x86_64", "myproject")
	q := req.URL.Query()
	q.Set("architecture", "x86_64")
	req.URL.RawQuery = q.Encode()

	w := httptest.NewRecorder()
	s.handler.ServeStatic(w, req)

	s.Equal(http.StatusOK, w.Code)

	var index flatpak.Index
	err := json.Unmarshal(w.Body.Bytes(), &index)
	s.NoError(err)
	s.Len(index.Results, 1)
	s.Len(index.Results[0].Images, 1)
	s.Equal("sha256:x86", index.Results[0].Images[0].Digest)
}

func (s *FlatpakHandlerTestSuite) TestLabelExistsQueryParam() {
	pro := &models.Project{
		ProjectID: 1,
		Name:      "myproject",
		Metadata:  map[string]string{"flatpak_index_enabled": "true"},
	}
	s.proCtl.On("GetByName", mock.Anything, "myproject", mock.Anything).Return(pro, nil)
	s.repoCtl.On("List", mock.Anything, mock.Anything).Return([]*repomodel.RepoRecord{
		{RepositoryID: 1, Name: "myproject/app"},
	}, nil)

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:flatpak1",
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/x86_64/stable",
				},
				ExtraAttrs: map[string]any{"architecture": "amd64"},
			},
			Tags: []*ctltag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	// Simulate: label:org.flatpak.ref:exists=1
	req := s.newRequest(http.MethodGet, "/myproject/index/static", "myproject")
	q := req.URL.Query()
	q.Set("label:org.flatpak.ref:exists", "1")
	req.URL.RawQuery = q.Encode()

	w := httptest.NewRecorder()
	s.handler.ServeStatic(w, req)

	s.Equal(http.StatusOK, w.Code)

	var index flatpak.Index
	err := json.Unmarshal(w.Body.Bytes(), &index)
	s.NoError(err)
	s.Len(index.Results, 1)
	s.Len(index.Results[0].Images, 1)
}

func TestParseQueryParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test/index/static?architecture=arm64&os=linux&tag=latest&label%3Aorg.flatpak.ref%3Aexists=1&annotation%3Aorg.example%3Aexists=1", nil)
	params := parseQueryParams(req)

	assert.Equal(t, []string{"arm64"}, params.Architecture)
	assert.Equal(t, []string{"linux"}, params.OS)
	assert.Equal(t, []string{"latest"}, params.Tag)
	assert.Len(t, params.Label, 1)
	assert.Equal(t, "org.flatpak.ref", params.Label[0].Key)
	assert.True(t, params.Label[0].ExistsOnly)
	assert.Len(t, params.Annotation, 1)
	assert.Equal(t, "org.example", params.Annotation[0].Key)
	assert.True(t, params.Annotation[0].ExistsOnly)
}

func TestParseLabelFilter(t *testing.T) {
	// exists filter
	f := parseLabelFilter("org.flatpak.ref:exists", "1")
	assert.Equal(t, "org.flatpak.ref", f.Key)
	assert.True(t, f.ExistsOnly)

	// value filter
	f = parseLabelFilter("org.flatpak.ref", "app/com.example.App/x86_64/stable")
	assert.Equal(t, "org.flatpak.ref", f.Key)
	assert.Equal(t, "app/com.example.App/x86_64/stable", f.Value)
	assert.False(t, f.ExistsOnly)
}

func TestFlatpakHandlerTestSuite(t *testing.T) {
	suite.Run(t, &FlatpakHandlerTestSuite{})
}

func TestNewFlatpakIndexHandler(t *testing.T) {
	h := NewFlatpakIndexHandler()
	assert.NotNil(t, h)
	assert.NotNil(t, h.builder)
}
