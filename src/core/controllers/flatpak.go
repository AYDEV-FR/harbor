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
	"net/http"
	"strings"

	"github.com/goharbor/harbor/src/core/api"
	"github.com/goharbor/harbor/src/lib/log"
)

// FlatpakController handles OCI Flatpak specification endpoints
type FlatpakController struct {
	api.BaseController
}

// FlatpakIndexResponse represents the response structure for Flatpak index endpoints
type FlatpakIndexResponse struct {
	Registry string                `json:"Registry"`
	Results  []FlatpakIndexResult `json:"Results"`
}

// FlatpakIndexResult represents a repository result in the index
type FlatpakIndexResult struct {
	Name   string             `json:"Name"`
	Images []FlatpakImageInfo `json:"Images"`
	Lists  []FlatpakListInfo  `json:"Lists"`
}

// FlatpakImageInfo represents image information in the index
type FlatpakImageInfo struct {
	Tags         []string          `json:"Tags,omitempty"`
	Digest       string            `json:"Digest"`
	MediaType    string            `json:"MediaType"`
	OS           string            `json:"OS,omitempty"`
	Architecture string            `json:"Architecture,omitempty"`
	Annotations  map[string]string `json:"Annotations,omitempty"`
	Labels       map[string]string `json:"Labels,omitempty"`
}

// FlatpakListInfo represents manifest list information in the index
type FlatpakListInfo struct {
	Tags      []string          `json:"Tags,omitempty"`
	Digest    string            `json:"Digest"`
	MediaType string            `json:"MediaType"`
	Labels    map[string]string `json:"Labels,omitempty"`
}

// Prepare inits the FlatpakController
func (f *FlatpakController) Prepare() {
	f.BaseController.Prepare()
}

// IndexStatic handles the /index/static endpoint
// Requests are expected to be repeated with exactly the same parameters
func (f *FlatpakController) IndexStatic() {
	f.handleIndexRequest(true)
}

// IndexDynamic handles the /index/dynamic endpoint
// Requests constructed via user interaction
func (f *FlatpakController) IndexDynamic() {
	f.handleIndexRequest(false)
}

// handleIndexRequest processes both static and dynamic index requests
func (f *FlatpakController) handleIndexRequest(isStatic bool) {
	// Parse query parameters
	filters := f.parseQueryFilters()

	// Get registry URL from request
	registryURL := f.getRegistryURL()

	// Build response
	response := FlatpakIndexResponse{
		Registry: registryURL,
		Results:  f.queryRepositories(filters),
	}

	// Set appropriate cache headers
	if isStatic {
		// Static requests should be cacheable
		f.Ctx.Output.Header("Cache-Control", "public, max-age=3600")
	} else {
		// Dynamic requests should not be cached
		f.Ctx.Output.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		f.Ctx.Output.Header("Pragma", "no-cache")
		f.Ctx.Output.Header("Expires", "0")
	}

	f.Ctx.Output.Header("Content-Type", "application/json")
	f.Data["json"] = response

	if err := f.ServeJSON(); err != nil {
		log.Errorf("Failed to serve JSON response: %v", err)
		f.CustomAbort(http.StatusInternalServerError, "Internal server error")
	}
}

// QueryFilters represents the parsed query parameters
type QueryFilters struct {
	Repository   []string
	Tag          []string
	OS           []string
	Architecture []string
	Annotations  map[string][]string
	Labels       map[string][]string
}

// parseQueryFilters extracts and parses query parameters according to the Flatpak OCI spec
func (f *FlatpakController) parseQueryFilters() QueryFilters {
	filters := QueryFilters{
		Annotations: make(map[string][]string),
		Labels:      make(map[string][]string),
	}

	// Parse standard parameters
	if repos := f.GetStrings("repository"); len(repos) > 0 {
		filters.Repository = repos
	}
	if tags := f.GetStrings("tag"); len(tags) > 0 {
		filters.Tag = tags
	}
	if oses := f.GetStrings("os"); len(oses) > 0 {
		filters.OS = oses
	}
	if archs := f.GetStrings("architecture"); len(archs) > 0 {
		filters.Architecture = archs
	}

	// Parse annotation and label parameters
	for key, values := range f.Ctx.Request.URL.Query() {
		if strings.HasPrefix(key, "annotation:") {
			annotationKey := strings.TrimPrefix(key, "annotation:")
			filters.Annotations[annotationKey] = values
		} else if strings.HasPrefix(key, "label:") {
			labelKey := strings.TrimPrefix(key, "label:")
			filters.Labels[labelKey] = values
		}
	}

	return filters
}

// getRegistryURL constructs the registry URL from the request
func (f *FlatpakController) getRegistryURL() string {
	scheme := "https"
	if f.Ctx.Request.TLS == nil {
		scheme = "http"
	}

	host := f.Ctx.Request.Host
	if host == "" {
		host = f.Ctx.Request.Header.Get("X-Forwarded-Host")
		if host == "" {
			host = "localhost"
		}
	}

	return scheme + "://" + host
}

// queryRepositories performs the actual repository query based on filters
func (f *FlatpakController) queryRepositories(filters QueryFilters) []FlatpakIndexResult {
	// TODO: Implement actual repository querying logic
	// This is a placeholder implementation that returns mock data
	// In a real implementation, this would:
	// 1. Query the Harbor registry database/API
	// 2. Apply the provided filters
	// 3. Return matching repositories with their images and manifests

	log.Infof("Querying repositories with filters: %+v", filters)

	// Mock response for demonstration
	results := []FlatpakIndexResult{
		{
			Name: "example-repo",
			Images: []FlatpakImageInfo{
				{
					Tags:         []string{"latest", "v1.0.0"},
					Digest:       "sha256:example1234567890abcdef",
					MediaType:    "application/vnd.oci.image.manifest.v1+json",
					OS:           "linux",
					Architecture: "amd64",
					Annotations: map[string]string{
						"org.flatpak.ref": "app/org.example.App/x86_64/stable",
					},
					Labels: map[string]string{
						"maintainer": "example@example.com",
					},
				},
			},
			Lists: []FlatpakListInfo{},
		},
	}

	return results
}