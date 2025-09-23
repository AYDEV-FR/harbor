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

	// Debug: log all query parameters
	log.Infof("Flatpak query parameters: %+v", f.Ctx.Request.URL.Query())

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
			log.Infof("Flatpak label filter found: key=%s, values=%v", labelKey, values)
			filters.Labels[labelKey] = values
		}
	}

	log.Infof("Flatpak parsed filters: %+v", filters)

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
		log.Infof("Flatpak label 'org.flatpak.ref:exists' found with values: %v", labelValues)
		for _, value := range labelValues {
			if value == "1" {
				hasFlatpakLabel = true
				break
			}
		}
	} else {
		log.Infof("Flatpak label 'org.flatpak.ref:exists' not found in filters")
	}

	// Only return results if looking for Flatpak apps
	if !hasFlatpakLabel {
		log.Infof("No Flatpak label filter found, returning empty results")
		return []FlatpakIndexResult{}
	}

	log.Infof("Flatpak label filter detected, proceeding with repository query")

	ctx := f.Ctx.Request.Context()
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

	log.Infof("Found %d repositories to check", len(repositories))

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
			WithTag:       true,
			WithLabel:     true,
			WithAccessory: true,
		})
		if err != nil {
			log.Errorf("Failed to query artifacts for repository %s: %v", repo.Name, err)
			continue
		}

		log.Infof("Found %d artifacts in repository %s", len(artifacts), repo.Name)

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
	log.Infof("Checking artifact %s for Flatpak labels, has %d labels, %d references", art.Digest, len(art.Labels), len(art.References))
	log.Infof("Artifact type: %s, media type: %s, manifest media type: %s", art.Type, art.MediaType, art.ManifestMediaType)
	log.Infof("IsImageIndex: %t", art.IsImageIndex())

	// Check for org.flatpak.ref in Harbor labels
	for _, label := range art.Labels {
		log.Infof("Artifact Harbor label: name=%s", label.Name)
		if label.Name == "org.flatpak.ref" {
			log.Infof("Found org.flatpak.ref in Harbor labels for artifact %s", art.Digest)
			return true
		}
	}

	// Check for org.flatpak.ref in OCI image labels (main location for Flatpak metadata)
	if art.ExtraAttrs != nil {
		if config, ok := art.ExtraAttrs["config"].(map[string]interface{}); ok {
			if ociLabels, exists := config["labels"].(map[string]interface{}); exists {
				for key := range ociLabels {
					log.Infof("Artifact OCI label: key=%s", key)
					if key == "org.flatpak.ref" {
						log.Infof("Found org.flatpak.ref in OCI labels for artifact %s", art.Digest)
						return true
					}
				}
			}
		}
	}

	// Check references (architecture-specific manifests) for Flatpak labels
	ctx := f.Ctx.Request.Context()

	// First, check existing References if populated
	for _, ref := range art.References {
		log.Infof("Checking reference artifact %d for Flatpak labels", ref.ChildID)

		// Get the referenced artifact with full metadata
		refArt, err := artifact.Ctl.Get(ctx, ref.ChildID, &artifact.Option{
			WithLabel:     true,
			WithAccessory: true,
		})
		if err != nil {
			log.Errorf("Failed to get referenced artifact %d: %v", ref.ChildID, err)
			continue
		}

		if f.checkArtifactForFlatpakLabels(refArt, fmt.Sprintf("reference artifact %d", ref.ChildID)) {
			return true
		}
	}

	// If no References found, manually query for child manifests
	// This handles cases where IsImageIndex() might be false but child manifests exist
	log.Infof("Manually querying for child manifests of artifact %s", art.Digest)

	childQuery := &q.Query{
		Keywords: map[string]interface{}{
			"ParentID": art.ID,
		},
	}

	childArtifacts, err := artifact.Ctl.List(ctx, childQuery, &artifact.Option{
		WithLabel:     true,
		WithAccessory: true,
	})
	if err != nil {
		log.Errorf("Failed to query child artifacts for %s: %v", art.Digest, err)
	} else {
		log.Infof("Found %d child artifacts for %s", len(childArtifacts), art.Digest)
		for _, childArt := range childArtifacts {
			log.Infof("Checking child artifact %s (ID: %d) for Flatpak labels", childArt.Digest, childArt.ID)
			if f.checkArtifactForFlatpakLabels(childArt, fmt.Sprintf("child artifact %s", childArt.Digest)) {
				return true
			}
		}
	}

	// Also check in annotations if available
	if art.ExtraAttrs != nil {
		if annotations, ok := art.ExtraAttrs["annotations"].(map[string]interface{}); ok {
			if _, exists := annotations["org.flatpak.ref"]; exists {
				log.Infof("Found org.flatpak.ref annotation in artifact %s", art.Digest)
				return true
			}
		}
	}

	log.Infof("No org.flatpak.ref label/annotation found in artifact %s or its references", art.Digest)
	return false
}

// checkArtifactForFlatpakLabels checks a single artifact for Flatpak labels in both Harbor and OCI labels
func (f *FlatpakController) checkArtifactForFlatpakLabels(art *artifact.Artifact, description string) bool {
	log.Infof("Checking %s (digest: %s) for Flatpak labels, has %d Harbor labels", description, art.Digest, len(art.Labels))
	log.Infof("%s type: %s, media type: %s, manifest media type: %s", description, art.Type, art.MediaType, art.ManifestMediaType)

	// Check Harbor labels
	for _, label := range art.Labels {
		log.Infof("%s Harbor label: name=%s", description, label.Name)
		if label.Name == "org.flatpak.ref" {
			log.Infof("Found org.flatpak.ref in Harbor labels for %s", description)
			return true
		}
	}

	// Check OCI labels in config
	if art.ExtraAttrs != nil {
		if config, ok := art.ExtraAttrs["config"].(map[string]interface{}); ok {
			if ociLabels, exists := config["labels"].(map[string]interface{}); exists {
				log.Infof("%s has %d OCI labels", description, len(ociLabels))
				for key := range ociLabels {
					log.Infof("%s OCI label: key=%s", description, key)
					if key == "org.flatpak.ref" {
						log.Infof("Found org.flatpak.ref in OCI labels for %s", description)
						return true
					}
				}
			} else {
				log.Infof("%s has config but no OCI labels", description)
			}
		} else {
			log.Infof("%s has ExtraAttrs but no config", description)
		}
	} else {
		log.Infof("%s has no ExtraAttrs", description)
	}

	log.Infof("No org.flatpak.ref label found in %s", description)
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
