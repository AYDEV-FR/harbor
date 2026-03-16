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
	"encoding/json"
	"net/http"
	"strings"

	"github.com/goharbor/harbor/src/controller/artifact"
	"github.com/goharbor/harbor/src/controller/project"
	"github.com/goharbor/harbor/src/controller/repository"
	"github.com/goharbor/harbor/src/lib/config"
	"github.com/goharbor/harbor/src/lib/errors"
	lib_http "github.com/goharbor/harbor/src/lib/http"
	"github.com/goharbor/harbor/src/lib/log"
	"github.com/goharbor/harbor/src/pkg/flatpak"
	"github.com/goharbor/harbor/src/server/router"
)

type flatpakIndexHandler struct {
	builder *flatpak.Builder
}

// NewFlatpakIndexHandler creates a handler for Flatpak OCI registry index endpoints
func NewFlatpakIndexHandler() *flatpakIndexHandler {
	return &flatpakIndexHandler{
		builder: flatpak.NewBuilder(project.Ctl, artifact.Ctl, repository.Ctl),
	}
}

// ServeStatic handles GET /<project>/index/static with caching headers
func (h *flatpakIndexHandler) ServeStatic(w http.ResponseWriter, r *http.Request) {
	h.serveIndex(w, r, true)
}

// ServeDynamic handles GET /<project>/index/dynamic with no-store cache headers
func (h *flatpakIndexHandler) ServeDynamic(w http.ResponseWriter, r *http.Request) {
	h.serveIndex(w, r, false)
}

// parseQueryParams parses Flatpak index query parameters from the URL.
// The spec uses colon-separated prefixes for label/annotation filters:
//   - label:<key>=<value>        → filter by label value
//   - label:<key>:exists=1       → filter by label existence
//   - annotation:<key>=<value>   → filter by annotation value
//   - annotation:<key>:exists=1  → filter by annotation existence
//   - architecture=<value>       → filter by OCI architecture (GOARCH)
//   - os=<value>                 → filter by OS (GOOS)
//   - tag=<value>                → filter by tag
func parseQueryParams(r *http.Request) flatpak.QueryParams {
	params := flatpak.QueryParams{
		Architecture: r.URL.Query()["architecture"],
		OS:           r.URL.Query()["os"],
		Tag:          r.URL.Query()["tag"],
	}

	for key, values := range r.URL.Query() {
		if strings.HasPrefix(key, "label:") {
			suffix := key[len("label:"):]
			for _, val := range values {
				params.Label = append(params.Label, parseLabelFilter(suffix, val))
			}
		} else if strings.HasPrefix(key, "annotation:") {
			suffix := key[len("annotation:"):]
			for _, val := range values {
				params.Annotation = append(params.Annotation, parseLabelFilter(suffix, val))
			}
		}
	}

	return params
}

// parseLabelFilter parses a label/annotation filter from the query parameter.
// suffix is the part after "label:" or "annotation:", e.g.:
//   - "org.flatpak.ref:exists" with value "1" → exists-only filter
//   - "org.flatpak.ref" with value "app/..." → exact value filter
func parseLabelFilter(suffix, value string) flatpak.LabelFilter {
	if strings.HasSuffix(suffix, ":exists") {
		return flatpak.LabelFilter{
			Key:        strings.TrimSuffix(suffix, ":exists"),
			ExistsOnly: true,
		}
	}
	return flatpak.LabelFilter{
		Key:   suffix,
		Value: value,
	}
}

func (h *flatpakIndexHandler) serveIndex(w http.ResponseWriter, r *http.Request, cacheable bool) {
	ctx := r.Context()
	projectName := router.Param(ctx, ":splat")
	if projectName == "" {
		lib_http.SendError(w, errors.NotFoundError(errors.New("project name is required")))
		return
	}

	params := parseQueryParams(r)

	registryURL, err := config.ExtEndpoint()
	if err != nil || registryURL == "" {
		registryURL = "https://" + r.Host
	}

	index, err := h.builder.Build(ctx, registryURL, projectName, params)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			lib_http.SendError(w, err)
			return
		}
		log.Errorf("failed to build flatpak index for project %s: %v", projectName, err)
		lib_http.SendError(w, err)
		return
	}

	if cacheable {
		w.Header().Set("Cache-Control", "max-age=120")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(index); err != nil {
		log.Errorf("failed to encode flatpak index response: %v", err)
	}
}
