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

package flatpak

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/goharbor/harbor/src/controller/artifact"
	"github.com/goharbor/harbor/src/controller/tag"
	"github.com/goharbor/harbor/src/lib/q"
	pkgart "github.com/goharbor/harbor/src/pkg/artifact"
	"github.com/goharbor/harbor/src/pkg/project/models"
	"github.com/goharbor/harbor/src/pkg/repository/model"
	tagmodel "github.com/goharbor/harbor/src/pkg/tag/model/tag"
	arttest "github.com/goharbor/harbor/src/testing/controller/artifact"
	protest "github.com/goharbor/harbor/src/testing/controller/project"
	repotest "github.com/goharbor/harbor/src/testing/controller/repository"
)

type IndexTestSuite struct {
	suite.Suite
	builder *Builder
	proCtl  *protest.Controller
	artCtl  *arttest.Controller
	repoCtl *repotest.Controller
}

func (s *IndexTestSuite) SetupTest() {
	s.proCtl = &protest.Controller{}
	s.artCtl = &arttest.Controller{}
	s.repoCtl = &repotest.Controller{}
	s.builder = NewBuilder(s.proCtl, s.artCtl, s.repoCtl)
}

func (s *IndexTestSuite) setupProject() {
	pro := &models.Project{
		ProjectID: 1,
		Name:      "test-project",
		Metadata: map[string]string{
			"flatpak_index_enabled": "true",
		},
	}
	s.proCtl.On("GetByName", mock.Anything, "test-project", mock.Anything).Return(pro, nil)
	s.repoCtl.On("List", mock.Anything, mock.Anything).Return([]*model.RepoRecord{
		{RepositoryID: 1, Name: "test-project/myapp"},
	}, nil)
}

func (s *IndexTestSuite) TestBuildWithZeroArtifacts() {
	s.setupProject()
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*artifact.Artifact{}, nil)

	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{})
	s.NoError(err)
	s.NotNil(index)
	s.Equal("https://harbor.example.com", index.Registry)
	s.Empty(index.Results)
}

func (s *IndexTestSuite) TestBuildFiltersNonFlatpakImages() {
	s.setupProject()

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:abc123",
				Size:              50000000,
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				RepositoryName:    "test-project/myapp",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/x86_64/stable",
				},
				ExtraAttrs: map[string]any{
					"architecture": "amd64",
					"os":           "linux",
				},
			},
			Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
		{
			Artifact: pkgart.Artifact{
				ID:                2,
				Digest:            "sha256:def456",
				Size:              30000000,
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				RepositoryName:    "test-project/other",
				Annotations:       map[string]string{},
			},
			Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "latest"}}},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{})
	s.NoError(err)
	s.Len(index.Results, 1)
	s.Equal("test-project/myapp", index.Results[0].Name)
	s.Len(index.Results[0].Images, 1)
	s.Equal("sha256:abc123", index.Results[0].Images[0].Digest)
	s.Equal("amd64", index.Results[0].Images[0].Architecture)
	s.Equal([]string{"stable"}, index.Results[0].Images[0].Tags)
}

func (s *IndexTestSuite) TestArchitectureFilter() {
	s.setupProject()

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:abc123",
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/x86_64/stable",
				},
				ExtraAttrs: map[string]any{"architecture": "amd64", "os": "linux"},
			},
			Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
		{
			Artifact: pkgart.Artifact{
				ID:                2,
				Digest:            "sha256:def456",
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/aarch64/stable",
				},
				ExtraAttrs: map[string]any{"architecture": "arm64", "os": "linux"},
			},
			Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	// filter for amd64 only
	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{
		Architecture: []string{"amd64"},
	})
	s.NoError(err)
	s.Len(index.Results, 1)
	s.Len(index.Results[0].Images, 1)
	s.Equal("sha256:abc123", index.Results[0].Images[0].Digest)
}

func (s *IndexTestSuite) TestTagFilter() {
	s.setupProject()

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:abc123",
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/x86_64/stable",
				},
				ExtraAttrs: map[string]any{"architecture": "amd64"},
			},
			Tags: []*tag.Tag{
				{Tag: tagmodel.Tag{Name: "stable"}},
				{Tag: tagmodel.Tag{Name: "latest"}},
			},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	// the tag filter applies to the image, both tags should appear since the artifact matches
	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{
		Tag: []string{"stable"},
	})
	s.NoError(err)
	s.Len(index.Results, 1)
	s.Len(index.Results[0].Images, 1)
	// Tags include all tags when the artifact has a matching tag
	s.Contains(index.Results[0].Images[0].Tags, "stable")
}

func (s *IndexTestSuite) TestLabelExistsFilter() {
	s.setupProject()

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:abc123",
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref": "app/com.example.App/x86_64/stable",
				},
				ExtraAttrs: map[string]any{"architecture": "amd64"},
			},
			Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	// exists filter
	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{
		Label: []LabelFilter{{Key: "org.flatpak.ref", ExistsOnly: true}},
	})
	s.NoError(err)
	s.Len(index.Results, 1)
}

func (s *IndexTestSuite) TestLabelValueFilter() {
	s.setupProject()

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:abc123",
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.flatpak.ref":                "app/com.example.App/x86_64/stable",
					"org.freedesktop.appstream.name": "Example App",
				},
				ExtraAttrs: map[string]any{"architecture": "amd64"},
			},
			Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	// non-matching value filter
	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{
		Label: []LabelFilter{{Key: "org.freedesktop.appstream.name", Value: "Other"}},
	})
	s.NoError(err)
	s.Empty(index.Results)
}

func (s *IndexTestSuite) TestFlatpakRefFromConfigLabels() {
	s.setupProject()

	arts := []*artifact.Artifact{
		{
			Artifact: pkgart.Artifact{
				ID:                1,
				Digest:            "sha256:config123",
				Size:              60000000,
				ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations:       map[string]string{},
				ExtraAttrs: map[string]any{
					"architecture": "amd64",
					"config": map[string]any{
						"Labels": map[string]any{
							"org.flatpak.ref": "app/com.config.App/x86_64/stable",
						},
					},
				},
			},
			Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "stable"}}},
		},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return(arts, nil)

	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{})
	s.NoError(err)
	s.Len(index.Results, 1)
	s.Len(index.Results[0].Images, 1)
	s.Equal("sha256:config123", index.Results[0].Images[0].Digest)
	s.Equal("app/com.config.App/x86_64/stable", index.Results[0].Images[0].Labels["org.flatpak.ref"])
}

func (s *IndexTestSuite) TestBuildWithImageIndex() {
	s.setupProject()

	indexArt := &artifact.Artifact{
		Artifact: pkgart.Artifact{
			ID:                1,
			Digest:            "sha256:index123",
			Size:              100,
			ManifestMediaType: "application/vnd.oci.image.index.v1+json",
			MediaType:         "application/vnd.oci.image.index.v1+json",
			References: []*pkgart.Reference{
				{ChildID: 10},
				{ChildID: 11},
			},
		},
		Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "latest"}}},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*artifact.Artifact{indexArt}, nil)

	childArm64 := &artifact.Artifact{
		Artifact: pkgart.Artifact{
			ID:                10,
			Digest:            "sha256:child_arm64",
			ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
			ExtraAttrs: map[string]any{
				"architecture": "arm64",
				"os":           "linux",
				"config": map[string]any{
					"Labels": map[string]any{
						"org.flatpak.ref": "app/org.gnome.Cheese/aarch64/stable",
					},
				},
			},
		},
	}
	childAmd64 := &artifact.Artifact{
		Artifact: pkgart.Artifact{
			ID:                11,
			Digest:            "sha256:child_amd64",
			ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
			ExtraAttrs: map[string]any{
				"architecture": "amd64",
				"os":           "linux",
				"config": map[string]any{
					"Labels": map[string]any{
						"org.flatpak.ref": "app/org.gnome.Cheese/x86_64/stable",
					},
				},
			},
		},
	}
	s.artCtl.On("Get", mock.Anything, int64(10), mock.Anything).Return(childArm64, nil)
	s.artCtl.On("Get", mock.Anything, int64(11), mock.Anything).Return(childAmd64, nil)

	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{})
	s.NoError(err)
	s.Len(index.Results, 1)
	// should be a Lists entry, not Images
	s.Empty(index.Results[0].Images)
	s.Len(index.Results[0].Lists, 1)
	s.Equal("sha256:index123", index.Results[0].Lists[0].Digest)
	s.Equal("application/vnd.oci.image.index.v1+json", index.Results[0].Lists[0].MediaType)
	s.Equal([]string{"latest"}, index.Results[0].Lists[0].Tags)
	s.Len(index.Results[0].Lists[0].Images, 2)
}

func (s *IndexTestSuite) TestBuildImageIndexWithArchFilter() {
	s.setupProject()

	indexArt := &artifact.Artifact{
		Artifact: pkgart.Artifact{
			ID:                1,
			Digest:            "sha256:index123",
			ManifestMediaType: "application/vnd.oci.image.index.v1+json",
			MediaType:         "application/vnd.oci.image.index.v1+json",
			References: []*pkgart.Reference{
				{ChildID: 10},
				{ChildID: 11},
			},
		},
		Tags: []*tag.Tag{{Tag: tagmodel.Tag{Name: "latest"}}},
	}
	s.artCtl.On("List", mock.Anything, mock.Anything, mock.Anything).Return([]*artifact.Artifact{indexArt}, nil)

	childArm64 := &artifact.Artifact{
		Artifact: pkgart.Artifact{
			ID:                10,
			Digest:            "sha256:child_arm64",
			ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
			ExtraAttrs: map[string]any{
				"architecture": "arm64",
				"os":           "linux",
				"config": map[string]any{
					"Labels": map[string]any{
						"org.flatpak.ref": "app/org.gnome.Cheese/aarch64/stable",
					},
				},
			},
		},
	}
	childAmd64 := &artifact.Artifact{
		Artifact: pkgart.Artifact{
			ID:                11,
			Digest:            "sha256:child_amd64",
			ManifestMediaType: "application/vnd.oci.image.manifest.v1+json",
			ExtraAttrs: map[string]any{
				"architecture": "amd64",
				"os":           "linux",
				"config": map[string]any{
					"Labels": map[string]any{
						"org.flatpak.ref": "app/org.gnome.Cheese/x86_64/stable",
					},
				},
			},
		},
	}
	s.artCtl.On("Get", mock.Anything, int64(10), mock.Anything).Return(childArm64, nil)
	s.artCtl.On("Get", mock.Anything, int64(11), mock.Anything).Return(childAmd64, nil)

	index, err := s.builder.Build(context.Background(), "https://harbor.example.com", "test-project", QueryParams{
		Architecture: []string{"arm64"},
	})
	s.NoError(err)
	s.Len(index.Results, 1)
	s.Len(index.Results[0].Lists, 1)
	s.Len(index.Results[0].Lists[0].Images, 1)
	s.Equal("sha256:child_arm64", index.Results[0].Lists[0].Images[0].Digest)
}

func TestExtractArchFromRef(t *testing.T) {
	assert.Equal(t, "x86_64", extractArchFromRef("app/com.example.App/x86_64/stable"))
	assert.Equal(t, "aarch64", extractArchFromRef("runtime/org.freedesktop.Platform/aarch64/23.08"))
	assert.Equal(t, "", extractArchFromRef("invalid"))
}

func TestExtractNameFromRef(t *testing.T) {
	assert.Equal(t, "com.example.App", extractNameFromRef("app/com.example.App/x86_64/stable"))
	assert.Equal(t, "org.freedesktop.Platform", extractNameFromRef("runtime/org.freedesktop.Platform/aarch64/23.08"))
}

func TestCollectLabels(t *testing.T) {
	art := &artifact.Artifact{
		Artifact: pkgart.Artifact{
			Annotations: map[string]string{
				"org.flatpak.ref": "app/com.example.App/x86_64/stable",
			},
			ExtraAttrs: map[string]any{
				"config": map[string]any{
					"Labels": map[string]any{
						"org.flatpak.installed-size": "150000000",
						"org.flatpak.ref":            "should-not-override",
					},
				},
			},
		},
	}

	labels := collectLabels(art)
	assert.Equal(t, "app/com.example.App/x86_64/stable", labels["org.flatpak.ref"])
	assert.Equal(t, "150000000", labels["org.flatpak.installed-size"])
}

func TestMatchesLabelFilters(t *testing.T) {
	labels := map[string]string{
		"org.flatpak.ref":                "app/com.example.App/x86_64/stable",
		"org.freedesktop.appstream.name": "Example",
	}

	// no filters
	assert.True(t, matchesLabelFilters(labels, nil))

	// exists filter
	assert.True(t, matchesLabelFilters(labels, []LabelFilter{
		{Key: "org.flatpak.ref", ExistsOnly: true},
	}))

	// exists filter for missing key
	assert.False(t, matchesLabelFilters(labels, []LabelFilter{
		{Key: "missing.key", ExistsOnly: true},
	}))

	// value match
	assert.True(t, matchesLabelFilters(labels, []LabelFilter{
		{Key: "org.freedesktop.appstream.name", Value: "Example"},
	}))

	// value mismatch
	assert.False(t, matchesLabelFilters(labels, []LabelFilter{
		{Key: "org.freedesktop.appstream.name", Value: "Other"},
	}))
}

func TestIndexTestSuite(t *testing.T) {
	suite.Run(t, &IndexTestSuite{})
}

var _ = func() {
	var proCtl *protest.Controller
	_ = proCtl
	var artCtl *arttest.Controller
	_ = artCtl
	var repoCtl *repotest.Controller
	_ = repoCtl
	_ = &q.Query{}
}
