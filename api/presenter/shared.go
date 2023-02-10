package presenter

import (
	"net/url"
	"path"
)

type Lifecycle struct {
	Type string        `json:"type"`
	Data LifecycleData `json:"data"`
}

type LifecycleData struct {
	Buildpacks []string `json:"buildpacks"`
	Stack      string   `json:"stack"`
}

type Relationships map[string]Relationship

type Relationship struct {
	Data *RelationshipData `json:"data"`
}

type RelationshipData struct {
	GUID string `json:"guid"`
}

type Metadata struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type Link struct {
	HRef   string `json:"href,omitempty"`
	Method string `json:"method,omitempty"`
}

type ListResponse struct {
	PaginationData PaginationData `json:"pagination"`
	Resources      []interface{}  `json:"resources"`
	Included       *IncludedData  `json:"included,omitempty"`
}

type PaginationData struct {
	TotalResults int     `json:"total_results"`
	TotalPages   int     `json:"total_pages"`
	First        PageRef `json:"first"`
	Last         PageRef `json:"last"`
	Next         *int    `json:"next"`
	Previous     *int    `json:"previous"`
}

type IncludedData struct {
	Apps             []interface{} `json:"apps,omitempty"`
	ServiceInstances []interface{} `json:"service_instances,omitempty"`
}

type PageRef struct {
	HREF string `json:"href"`
}

func ForList(resources []interface{}, baseURL, requestURL url.URL) ListResponse {
	return ListResponse{
		PaginationData: PaginationData{
			TotalResults: len(resources),
			TotalPages:   1,
			First: PageRef{
				HREF: buildURL(baseURL).appendPath(requestURL.Path).setQuery(requestURL.RawQuery).build(),
			},
			Last: PageRef{
				HREF: buildURL(baseURL).appendPath(requestURL.Path).setQuery(requestURL.RawQuery).build(),
			},
		},
		Resources: resources,
	}
}

type buildURL url.URL

func (u buildURL) appendPath(subpath ...string) buildURL {
	rest := path.Join(subpath...)
	if u.Path == "" {
		u.Path = rest
	} else {
		u.Path = path.Join(u.Path, rest)
	}

	return u
}

func (u buildURL) setQuery(rawQuery string) buildURL {
	u.RawQuery = rawQuery

	return u
}

func (u buildURL) build() string {
	native := url.URL(u)
	nativeP := &native

	return nativeP.String()
}

func emptyMapIfNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func emptySliceIfNil(m []string) []string {
	if m == nil {
		return []string{}
	}
	return m
}
