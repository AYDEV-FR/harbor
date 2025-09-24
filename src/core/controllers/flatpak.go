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
	"strings"

	"github.com/goharbor/harbor/src/controller/artifact"
	"github.com/goharbor/harbor/src/controller/repository"
	"github.com/goharbor/harbor/src/core/api"
	"github.com/goharbor/harbor/src/lib/log"
	"github.com/goharbor/harbor/src/lib/q"
	"github.com/goharbor/harbor/src/pkg/repository/model"
)

// FlatpakController handles OCI Flatpak specification endpoints
type FlatpakController struct {
	api.BaseController
}

// FlatpakIndexResponse represents the response structure for Flatpak index endpoints
type FlatpakIndexResponse struct {
	Registry string         `json:"Registry"`
	Results  []FlatpakEntry `json:"Results"`
}

// FlatpakEntry represents a single Flatpak application entry
type FlatpakEntry struct {
	Name   string         `json:"Name"`
	Images []FlatpakImage `json:"Images"`
}

// FlatpakImage represents an OCI image within a Flatpak entry
type FlatpakImage struct {
	Architecture string            `json:"Architecture"`
	Digest       string            `json:"Digest"`
	Labels       map[string]string `json:"Labels"`
	MediaType    string            `json:"MediaType"`
	OS           string            `json:"OS"`
	Tags         []string          `json:"Tags,omitempty"`
}

// GetStatic handles the /index/static endpoint
func (f *FlatpakController) GetStatic() {
	f.handleIndexRequest()
}

// GetDynamic handles the /index/dynamic endpoint
func (f *FlatpakController) GetDynamic() {
	f.handleIndexRequest()
}

// handleIndexRequest processes both static and dynamic index requests
func (f *FlatpakController) handleIndexRequest() {
	// Get the registry base URL from request
	registryURL := fmt.Sprintf("%s://%s", f.Ctx.Input.Scheme(), f.Ctx.Input.Host())

	response := FlatpakIndexResponse{
		Registry: registryURL,
		Results:  []FlatpakEntry{},
	}

	// Parse query parameters
	filters := f.parseQueryFilters()
	log.Infof("Flatpak query filters: %+v", filters)

	// Must have label:org.flatpak.ref:exists parameter
	if !f.hasRequiredFlatpakFilter(filters) {
		log.Infof("Missing required label:org.flatpak.ref:exists filter")
		f.Data["json"] = response
		f.ServeJSON()
		return
	}

	// Find repositories that might contain Flatpak artifacts
	repos, err := f.findAllRepositories()
	if err != nil {
		log.Errorf("Error finding repositories: %v", err)
		f.Data["json"] = response
		f.ServeJSON()
		return
	}

	log.Infof("Found %d repositories to check", len(repos))

	// Process each repository for Flatpak artifacts
	for _, repo := range repos {
		artifacts, err := f.findFlatpakArtifacts(repo, filters)
		if err != nil {
			log.Errorf("Error finding artifacts in repository %s: %v", repo.Name, err)
			continue
		}

		log.Infof("Found %d Flatpak artifacts in repository %s", len(artifacts), repo.Name)

		// Group artifacts by Flatpak application name
		appGroups := f.groupArtifactsByApp(artifacts, filters)

		for appName, appArtifacts := range appGroups {
			entry := FlatpakEntry{
				Name:   appName,
				Images: []FlatpakImage{},
			}

			for _, art := range appArtifacts {
				images := f.convertArtifactToImages(repo, art, filters)
				entry.Images = append(entry.Images, images...)
			}

			if len(entry.Images) > 0 {
				response.Results = append(response.Results, entry)
			}
		}
	}

	log.Infof("Returning %d Flatpak entries", len(response.Results))
	f.Data["json"] = response
	f.ServeJSON()
}

// parseQueryFilters extracts and parses query parameters
func (f *FlatpakController) parseQueryFilters() map[string][]string {
	filters := make(map[string][]string)

	// Get all query parameters
	for key := range f.Ctx.Request.URL.Query() {
		values := f.Ctx.Input.Query(key)

		// Handle label filters (they come as "label:key:exists")
		if strings.HasPrefix(key, "label:") && strings.HasSuffix(key, ":exists") {
			labelKey := strings.TrimSuffix(strings.TrimPrefix(key, "label:"), ":exists")
			if _, exists := filters["labels"]; !exists {
				filters["labels"] = []string{}
			}
			filters["labels"] = append(filters["labels"], labelKey)
			log.Infof("Flatpak label filter: %s", labelKey)
		} else if values != "" {
			// Handle other filters (architecture, os, tag, etc.)
			filterValues := strings.Split(values, ",")
			filters[key] = filterValues
			log.Infof("Flatpak filter: %s=%v", key, filterValues)
		}
	}

	return filters
}

// hasRequiredFlatpakFilter checks if the required org.flatpak.ref:exists filter is present
func (f *FlatpakController) hasRequiredFlatpakFilter(filters map[string][]string) bool {
	labels, exists := filters["labels"]
	if !exists {
		return false
	}

	for _, label := range labels {
		if label == "org.flatpak.ref" {
			return true
		}
	}

	return false
}

// findAllRepositories gets all repositories
func (f *FlatpakController) findAllRepositories() ([]*model.RepoRecord, error) {
	repoCtl := repository.Ctl
	query := &q.Query{}

	return repoCtl.List(f.Ctx.Request.Context(), query)
}

// findFlatpakArtifacts finds artifacts in a repository that have Flatpak labels
func (f *FlatpakController) findFlatpakArtifacts(repo *model.RepoRecord, filters map[string][]string) ([]*artifact.Artifact, error) {
	artCtl := artifact.Ctl

	// Create query for artifacts in this repository
	query := &q.Query{
		Keywords: map[string]interface{}{
			"repository_id": repo.RepositoryID,
		},
	}

	// Apply tag filter if specified
	if tags, exists := filters["tag"]; exists && len(tags) > 0 {
		query.Keywords["tag"] = map[string]interface{}{
			"$in": tags,
		}
	}

	// Get all artifacts in the repository
	artifacts, err := artCtl.List(f.Ctx.Request.Context(), query, nil)
	if err != nil {
		return nil, err
	}

	var flatpakArtifacts []*artifact.Artifact

	// Filter artifacts that have Flatpak labels
	for _, art := range artifacts {
		if f.isFlatpakArtifact(art) {
			flatpakArtifacts = append(flatpakArtifacts, art)
		}
	}

	return flatpakArtifacts, nil
}

// isFlatpakArtifact checks if an artifact is a Flatpak artifact
func (f *FlatpakController) isFlatpakArtifact(art *artifact.Artifact) bool {
	// Check if this artifact or its references have org.flatpak.ref label
	return f.artifactHasLabel(art, "org.flatpak.ref")
}

// artifactHasLabel checks if an artifact or its references have a specific label
func (f *FlatpakController) artifactHasLabel(art *artifact.Artifact, labelKey string) bool {
	// Check main artifact
	if f.hasLabel(art, labelKey) {
		return true
	}

	// Check references (for image indexes) - need to fetch child artifacts separately
	if len(art.References) > 0 {
		artCtl := artifact.Ctl
		for _, ref := range art.References {
			// Get the child artifact by its ID
			childArt, err := artCtl.Get(f.Ctx.Request.Context(), ref.ChildID, nil)
			if err != nil {
				log.Errorf("Error getting child artifact %d: %v", ref.ChildID, err)
				continue
			}
			if f.hasLabel(childArt, labelKey) {
				return true
			}
		}
	}

	return false
}

// hasLabel checks if a specific artifact has a label in its config or Harbor labels
func (f *FlatpakController) hasLabel(art *artifact.Artifact, labelKey string) bool {
	// Check in OCI image config Labels
	if art.ExtraAttrs != nil {
		if configData, ok := art.ExtraAttrs["config"].(map[string]interface{}); ok {
			if labels, exists := configData["Labels"]; exists {
				// Try both string and interface maps
				if labelMap, ok := labels.(map[string]string); ok {
					if _, hasLabel := labelMap[labelKey]; hasLabel {
						return true
					}
				}
				if labelMap, ok := labels.(map[string]interface{}); ok {
					if _, hasLabel := labelMap[labelKey]; hasLabel {
						return true
					}
				}
			}
		}
	}

	// Check Harbor labels as fallback
	if art.Labels != nil {
		for _, label := range art.Labels {
			if label.Name == labelKey {
				return true
			}
		}
	}

	return false
}

// groupArtifactsByApp groups artifacts by their Flatpak application name
func (f *FlatpakController) groupArtifactsByApp(artifacts []*artifact.Artifact, filters map[string][]string) map[string][]*artifact.Artifact {
	groups := make(map[string][]*artifact.Artifact)

	for _, art := range artifacts {
		appName := f.extractAppName(art)
		if appName == "" {
			continue
		}

		groups[appName] = append(groups[appName], art)
	}

	return groups
}

// extractAppName extracts the application name from the org.flatpak.ref label
func (f *FlatpakController) extractAppName(art *artifact.Artifact) string {
	flatpakRef := f.getLabel(art, "org.flatpak.ref")
	if flatpakRef == "" {
		return ""
	}

	// Parse Flatpak ref format: app/com.example.App/arch/branch or runtime/name/arch/branch
	parts := strings.Split(flatpakRef, "/")
	if len(parts) >= 2 {
		// For apps, use the app ID (e.g., "com.example.App")
		// For runtimes, use the runtime name
		if parts[0] == "app" && len(parts) >= 2 {
			return parts[1]
		}
		if parts[0] == "runtime" && len(parts) >= 2 {
			return parts[1]
		}
	}

	return ""
}

// convertArtifactToImages converts an artifact to Flatpak images
func (f *FlatpakController) convertArtifactToImages(repo *model.RepoRecord, art *artifact.Artifact, filters map[string][]string) []FlatpakImage {
	var images []FlatpakImage

	// Handle image index - process references
	if art.ManifestMediaType == "application/vnd.oci.image.index.v1+json" && len(art.References) > 0 {
		artCtl := artifact.Ctl
		for _, ref := range art.References {
			// Get the child artifact by its ID
			childArt, err := artCtl.Get(f.Ctx.Request.Context(), ref.ChildID, nil)
			if err != nil {
				log.Errorf("Error getting child artifact %d: %v", ref.ChildID, err)
				continue
			}
			if image := f.convertSingleArtifactToImage(repo, childArt, filters); image != nil {
				images = append(images, *image)
			}
		}
	} else {
		// Handle single image
		if image := f.convertSingleArtifactToImage(repo, art, filters); image != nil {
			images = append(images, *image)
		}
	}

	return images
}

// convertSingleArtifactToImage converts a single artifact to a Flatpak image
func (f *FlatpakController) convertSingleArtifactToImage(repo *model.RepoRecord, art *artifact.Artifact, filters map[string][]string) *FlatpakImage {
	// Get architecture and OS from artifact
	arch := f.getArchitecture(art)
	os := f.getOS(art)

	// Apply architecture filter
	if archFilters, exists := filters["architecture"]; exists && len(archFilters) > 0 {
		if !f.stringInSlice(arch, archFilters) {
			return nil
		}
	}

	// Apply OS filter
	if osFilters, exists := filters["os"]; exists && len(osFilters) > 0 {
		if !f.stringInSlice(os, osFilters) {
			return nil
		}
	}

	// Get all labels from the artifact
	labels := f.getAllLabels(art)

	// Get tags for this artifact
	tags := f.getArtifactTags(art)

	image := &FlatpakImage{
		Architecture: arch,
		Digest:       art.Digest,
		Labels:       labels,
		MediaType:    art.ManifestMediaType,
		OS:           os,
		Tags:         tags,
	}

	return image
}

// getLabel retrieves a specific label from an artifact
func (f *FlatpakController) getLabel(art *artifact.Artifact, key string) string {
	// Check in OCI image config Labels
	if art.ExtraAttrs != nil {
		if configData, ok := art.ExtraAttrs["config"].(map[string]interface{}); ok {
			if labels, exists := configData["Labels"]; exists {
				if labelMap, ok := labels.(map[string]string); ok {
					if value, exists := labelMap[key]; exists {
						return value
					}
				}
				if labelMap, ok := labels.(map[string]interface{}); ok {
					if value, exists := labelMap[key]; exists {
						if strValue, ok := value.(string); ok {
							return strValue
						}
					}
				}
			}
		}
	}

	// Check Harbor labels as fallback
	if art.Labels != nil {
		for _, label := range art.Labels {
			if label.Name == key {
				return label.Description // Use Description as value since there's no Value field
			}
		}
	}

	return ""
}

// getAllLabels retrieves all labels from an artifact
func (f *FlatpakController) getAllLabels(art *artifact.Artifact) map[string]string {
	labels := make(map[string]string)

	// Get from OCI image config Labels
	if art.ExtraAttrs != nil {
		if configData, ok := art.ExtraAttrs["config"].(map[string]interface{}); ok {
			if configLabels, exists := configData["Labels"]; exists {
				if labelMap, ok := configLabels.(map[string]string); ok {
					for k, v := range labelMap {
						labels[k] = v
					}
				}
				if labelMap, ok := configLabels.(map[string]interface{}); ok {
					for k, v := range labelMap {
						if strValue, ok := v.(string); ok {
							labels[k] = strValue
						}
					}
				}
			}
		}
	}

	// Add Harbor labels
	if art.Labels != nil {
		for _, label := range art.Labels {
			labels[label.Name] = label.Description // Use Description as value
		}
	}

	return labels
}

// getArchitecture retrieves the architecture from an artifact
func (f *FlatpakController) getArchitecture(art *artifact.Artifact) string {
	if art.ExtraAttrs != nil {
		if arch, exists := art.ExtraAttrs["architecture"].(string); exists {
			return arch
		}
	}
	return "amd64" // default
}

// getOS retrieves the OS from an artifact
func (f *FlatpakController) getOS(art *artifact.Artifact) string {
	if art.ExtraAttrs != nil {
		if os, exists := art.ExtraAttrs["os"].(string); exists {
			return os
		}
	}
	return "linux" // default
}

// getArtifactTags gets the tags associated with an artifact
func (f *FlatpakController) getArtifactTags(art *artifact.Artifact) []string {
	var tags []string
	if art.Tags != nil {
		for _, tag := range art.Tags {
			tags = append(tags, tag.Name)
		}
	}
	return tags
}

// stringInSlice checks if a string is in a slice of strings
func (f *FlatpakController) stringInSlice(str string, slice []string) bool {
	for _, item := range slice {
		if item == str {
			return true
		}
	}
	return false
}
