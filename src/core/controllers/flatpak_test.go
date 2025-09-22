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

package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/beego/beego/v2/server/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Register Flatpak routes for testing
	web.Router("/index/static", &FlatpakController{}, "get:IndexStatic")
	web.Router("/index/dynamic", &FlatpakController{}, "get:IndexDynamic")
}

func TestFlatpakIndexStatic(t *testing.T) {
	assert := assert.New(t)

	// Test basic static endpoint
	r, _ := http.NewRequest("GET", "/index/static", nil)
	w := httptest.NewRecorder()
	web.BeeApp.Handlers.ServeHTTP(w, r)

	assert.Equal(http.StatusOK, w.Code, "Static index endpoint should return 200")
	assert.Equal("application/json", w.Header().Get("Content-Type"), "Content-Type should be application/json")

	// Check cache headers for static endpoint
	cacheControl := w.Header().Get("Cache-Control")
	assert.Contains(cacheControl, "public", "Static endpoint should have public cache control")
	assert.Contains(cacheControl, "max-age", "Static endpoint should have max-age set")

	// Verify response structure
	var response FlatpakIndexResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	assert.NotEmpty(response.Registry, "Registry field should not be empty")
	assert.NotNil(response.Results, "Results field should not be nil")
}

func TestFlatpakIndexDynamic(t *testing.T) {
	assert := assert.New(t)

	// Test basic dynamic endpoint
	r, _ := http.NewRequest("GET", "/index/dynamic", nil)
	w := httptest.NewRecorder()
	web.BeeApp.Handlers.ServeHTTP(w, r)

	assert.Equal(http.StatusOK, w.Code, "Dynamic index endpoint should return 200")
	assert.Equal("application/json", w.Header().Get("Content-Type"), "Content-Type should be application/json")

	// Check cache headers for dynamic endpoint
	cacheControl := w.Header().Get("Cache-Control")
	assert.Contains(cacheControl, "no-cache", "Dynamic endpoint should have no-cache control")
	assert.Contains(cacheControl, "no-store", "Dynamic endpoint should have no-store control")
	assert.Equal("no-cache", w.Header().Get("Pragma"), "Dynamic endpoint should have Pragma: no-cache")
	assert.Equal("0", w.Header().Get("Expires"), "Dynamic endpoint should have Expires: 0")

	// Verify response structure
	var response FlatpakIndexResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	assert.NotEmpty(response.Registry, "Registry field should not be empty")
	assert.NotNil(response.Results, "Results field should not be nil")
}

func TestFlatpakIndexWithQueryParameters(t *testing.T) {
	assert := assert.New(t)

	// Test with query parameters
	r, _ := http.NewRequest("GET", "/index/static?repository=test-repo&tag=latest&os=linux&architecture=amd64", nil)
	w := httptest.NewRecorder()
	web.BeeApp.Handlers.ServeHTTP(w, r)

	assert.Equal(http.StatusOK, w.Code, "Static index endpoint with query params should return 200")

	var response FlatpakIndexResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	assert.NotEmpty(response.Registry, "Registry field should not be empty")
	assert.NotNil(response.Results, "Results field should not be nil")
}

func TestFlatpakIndexWithAnnotationsAndLabels(t *testing.T) {
	assert := assert.New(t)

	// Test with annotation and label parameters
	r, _ := http.NewRequest("GET", "/index/static?annotation:org.flatpak.ref=app/test&label:maintainer=test@example.com", nil)
	w := httptest.NewRecorder()
	web.BeeApp.Handlers.ServeHTTP(w, r)

	assert.Equal(http.StatusOK, w.Code, "Static index endpoint with annotations/labels should return 200")

	var response FlatpakIndexResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	assert.NotEmpty(response.Registry, "Registry field should not be empty")
	assert.NotNil(response.Results, "Results field should not be nil")
}

func TestFlatpakIndexMultipleValues(t *testing.T) {
	assert := assert.New(t)

	// Test with multiple values for the same parameter (OR behavior)
	r, _ := http.NewRequest("GET", "/index/static?repository=repo1&repository=repo2&tag=v1&tag=v2", nil)
	w := httptest.NewRecorder()
	web.BeeApp.Handlers.ServeHTTP(w, r)

	assert.Equal(http.StatusOK, w.Code, "Static index endpoint with multiple values should return 200")

	var response FlatpakIndexResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	assert.NotEmpty(response.Registry, "Registry field should not be empty")
	assert.NotNil(response.Results, "Results field should not be nil")
}

func TestFlatpakResponseStructure(t *testing.T) {
	assert := assert.New(t)

	r, _ := http.NewRequest("GET", "/index/static", nil)
	w := httptest.NewRecorder()
	web.BeeApp.Handlers.ServeHTTP(w, r)

	var response FlatpakIndexResponse
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Verify response structure matches OCI Flatpak spec
	assert.NotEmpty(response.Registry, "Registry field should be present")
	assert.IsType([]FlatpakIndexResult{}, response.Results, "Results should be array of FlatpakIndexResult")

	// If there are results, verify their structure
	if len(response.Results) > 0 {
		result := response.Results[0]
		assert.NotEmpty(result.Name, "Result should have a name")
		assert.IsType([]FlatpakImageInfo{}, result.Images, "Images should be array of FlatpakImageInfo")
		assert.IsType([]FlatpakListInfo{}, result.Lists, "Lists should be array of FlatpakListInfo")

		// If there are images, verify their structure
		if len(result.Images) > 0 {
			image := result.Images[0]
			assert.NotEmpty(image.Digest, "Image should have a digest")
			assert.NotEmpty(image.MediaType, "Image should have a media type")
		}
	}
}