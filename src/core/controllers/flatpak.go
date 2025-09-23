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
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/goharbor/harbor/src/controller/artifact"
	"github.com/goharbor/harbor/src/controller/repository"
	"github.com/goharbor/harbor/src/core/api"
	"github.com/goharbor/harbor/src/lib/log"
	"github.com/goharbor/harbor/src/lib/q"
)

// FlatpakController handles OCI Flatpak specification endpoints
type FlatpakController struct {
	api.BaseController
}

// FlatpakIndexResponse represents the response structure for Flatpak index endpoints
type FlatpakIndexResponse struct {
	Registry string               `json:"Registry"`
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
	filters, err := f.parseQueryFilters()
	if err != nil {
		f.CustomAbort(http.StatusBadRequest, err.Error())
		return
	}

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
	f.Ctx.Output.Header("Content-Encoding", "gzip")
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
func (f *FlatpakController) parseQueryFilters() (QueryFilters, error) {
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
			// Following Pulp's approach: annotations are not supported
			return filters, fmt.Errorf("annotation queries are not supported")
		} else if strings.HasPrefix(key, "label:") {
			labelKey := strings.TrimPrefix(key, "label:")
			filters.Labels[labelKey] = values
		}
	}

	return filters, nil
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
	log.Infof("Querying repositories with filters: %+v", filters)

	// Check if request is looking for org.flatpak.ref labels
	hasFlatpakLabel := false
	if labelValues, exists := filters.Labels["org.flatpak.ref:exists"]; exists {
		for _, value := range labelValues {
			if value == "1" {
				hasFlatpakLabel = true
				break
			}
		}
	}

	// Only return results if looking for Flatpak apps
	if !hasFlatpakLabel {
		return []FlatpakIndexResult{}
	}

	ctx := context.Background()
	var results []FlatpakIndexResult

	// Query repositories from Harbor
	repoQuery := &q.Query{}
	if len(filters.Repository) > 0 {
		// Filter by repository names if specified
		repoQuery.Keywords = map[string]interface{}{
			"name": &q.FuzzyMatchValue{Value: strings.Join(filters.Repository, "|")},
		}
	}

	repositories, err := repository.Ctl.List(ctx, repoQuery)
	if err != nil {
		log.Errorf("Failed to query repositories: %v", err)
		return results
	}

	// For each repository, query artifacts
	for _, repo := range repositories {
		artifactQuery := &q.Query{
			Keywords: map[string]interface{}{
				"repository_id": repo.RepositoryID,
			},
		}

		// Apply tag filter if specified
		if len(filters.Tag) > 0 {
			artifactQuery.Keywords["tags"] = &q.FuzzyMatchValue{Value: strings.Join(filters.Tag, "|")}
		}

		artifacts, err := artifact.Ctl.List(ctx, artifactQuery, &artifact.Option{
			WithTag:   true,
			WithLabel: true,
		})
		if err != nil {
			log.Errorf("Failed to query artifacts for repository %s: %v", repo.Name, err)
			continue
		}

		var images []FlatpakImageInfo
		var lists []FlatpakListInfo

		for _, art := range artifacts {
			// Check if artifact has Flatpak labels
			if !f.hasFlatpakLabels(art, filters) {
				continue
			}

			// Check OS/Architecture filters
			if !f.matchesOSArch(art, filters) {
				continue
			}

			// Extract tags
			var tags []string
			for _, tag := range art.Tags {
				tags = append(tags, tag.Name)
			}

			// Build image info
			imageInfo := FlatpakImageInfo{
				Tags:         tags,
				Digest:       art.Digest,
				MediaType:    art.MediaType,
				OS:           f.extractOS(art),
				Architecture: f.extractArchitecture(art),
				Annotations:  f.extractAnnotations(art),
				Labels:       f.extractLabels(art),
			}

			// Determine if it's a manifest list or single image
			if f.isManifestList(art) {
				lists = append(lists, FlatpakListInfo{
					Tags:      tags,
					Digest:    art.Digest,
					MediaType: art.MediaType,
					Labels:    f.extractLabels(art),
				})
			} else {
				images = append(images, imageInfo)
			}
		}

		// Only include repositories that have matching artifacts
		if len(images) > 0 || len(lists) > 0 {
			results = append(results, FlatpakIndexResult{
				Name:   repo.Name,
				Images: images,
				Lists:  lists,
			})
		}
	}

	return results
}

// hasFlatpakLabels checks if the artifact has the required Flatpak labels
func (f *FlatpakController) hasFlatpakLabels(art *artifact.Artifact, filters QueryFilters) bool {
	// Check for org.flatpak.ref label or annotation
	for _, label := range art.Labels {
		if label.Name == "org.flatpak.ref" {
			return true
		}
	}

	// Also check in annotations if available
	if art.ExtraAttrs != nil {
		if annotations, ok := art.ExtraAttrs["annotations"].(map[string]interface{}); ok {
			if _, exists := annotations["org.flatpak.ref"]; exists {
				return true
			}
		}
	}

	return false
}

// matchesOSArch checks if the artifact matches the requested OS/Architecture filters
func (f *FlatpakController) matchesOSArch(art *artifact.Artifact, filters QueryFilters) bool {
	artOS := f.extractOS(art)
	artArch := f.extractArchitecture(art)

	// Check OS filter
	if len(filters.OS) > 0 {
		found := false
		for _, filterOS := range filters.OS {
			if artOS == filterOS {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check Architecture filter
	if len(filters.Architecture) > 0 {
		found := false
		for _, filterArch := range filters.Architecture {
			if artArch == filterArch {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// extractOS extracts the OS from artifact metadata
func (f *FlatpakController) extractOS(art *artifact.Artifact) string {
	if art.ExtraAttrs != nil {
		if config, ok := art.ExtraAttrs["config"].(map[string]interface{}); ok {
			if os, exists := config["os"].(string); exists {
				return os
			}
		}
	}
	return "linux" // default
}

// extractArchitecture extracts the architecture from artifact metadata
func (f *FlatpakController) extractArchitecture(art *artifact.Artifact) string {
	if art.ExtraAttrs != nil {
		if config, ok := art.ExtraAttrs["config"].(map[string]interface{}); ok {
			if arch, exists := config["architecture"].(string); exists {
				return arch
			}
		}
	}
	return "amd64" // default
}

// extractAnnotations extracts annotations from artifact metadata
func (f *FlatpakController) extractAnnotations(art *artifact.Artifact) map[string]string {
	annotations := make(map[string]string)
	if art.ExtraAttrs != nil {
		if annots, ok := art.ExtraAttrs["annotations"].(map[string]interface{}); ok {
			for key, value := range annots {
				if strValue, ok := value.(string); ok {
					annotations[key] = strValue
				}
			}
		}
	}
	return annotations
}

// extractLabels extracts labels from artifact Harbor labels and metadata
func (f *FlatpakController) extractLabels(art *artifact.Artifact) map[string]string {
	labels := make(map[string]string)

	// Add Harbor labels (use Name as both key and indicate presence)
	for _, label := range art.Labels {
		labels[label.Name] = "true"
	}

	// Also check for OCI labels in config
	if art.ExtraAttrs != nil {
		if config, ok := art.ExtraAttrs["config"].(map[string]interface{}); ok {
			if ociLabels, exists := config["labels"].(map[string]interface{}); exists {
				for key, value := range ociLabels {
					if strValue, ok := value.(string); ok {
						labels[key] = strValue
					}
				}
			}
		}
	}

	return labels
}

// isManifestList determines if the artifact is a manifest list
func (f *FlatpakController) isManifestList(art *artifact.Artifact) bool {
	return art.MediaType == "application/vnd.oci.image.index.v1+json" ||
		art.MediaType == "application/vnd.docker.distribution.manifest.list.v2+json"
}
