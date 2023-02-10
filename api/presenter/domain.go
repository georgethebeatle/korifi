package presenter

import (
	"net/url"

	"code.cloudfoundry.org/korifi/api/repositories"
)

const (
	domainsBase = "/v3/domains"
)

type DomainResponse struct {
	Name               string   `json:"name"`
	GUID               string   `json:"guid"`
	Internal           bool     `json:"internal"`
	RouterGroup        *string  `json:"router_group"`
	SupportedProtocols []string `json:"supported_protocols"`

	CreatedAt     string              `json:"created_at"`
	UpdatedAt     string              `json:"updated_at"`
	Metadata      Metadata            `json:"metadata"`
	Relationships DomainRelationships `json:"relationships"`
	Links         DomainLinks         `json:"links"`
}

type DomainLinks struct {
	Self              Link  `json:"self"`
	RouteReservations Link  `json:"route_reservations"`
	RouterGroup       *Link `json:"router_group"`
}

type DomainRelationships struct {
	Organization        `json:"organization"`
	SharedOrganizations `json:"shared_organizations"`
}

type Organization struct {
	Data RelationshipData `json:"data,omitempty"`
}

type SharedOrganizations struct {
	Data []string `json:"data"`
}

func ForDomain(responseDomain repositories.DomainRecord, baseURL url.URL) DomainResponse {
	if responseDomain.Labels == nil {
		responseDomain.Labels = map[string]string{}
	}
	if responseDomain.Annotations == nil {
		responseDomain.Annotations = map[string]string{}
	}

	dr := DomainResponse{
		Name:               responseDomain.Name,
		GUID:               responseDomain.GUID,
		Internal:           false,
		RouterGroup:        nil,
		SupportedProtocols: []string{"http"},
		CreatedAt:          responseDomain.CreatedAt,
		UpdatedAt:          responseDomain.UpdatedAt,

		Metadata: Metadata{
			Labels:      responseDomain.Labels,
			Annotations: responseDomain.Annotations,
		},
		Relationships: DomainRelationships{
			Organization: Organization{
				Data: RelationshipData{GUID: responseDomain.OrgGUID},
			},
			SharedOrganizations: SharedOrganizations{
				Data: []string{},
			},
		},
		Links: DomainLinks{
			Self: Link{
				HRef: buildURL(baseURL).appendPath(domainsBase, responseDomain.GUID).build(),
			},
			RouteReservations: Link{
				HRef: buildURL(baseURL).appendPath(domainsBase, responseDomain.GUID, "route_reservations").build(),
			},
			RouterGroup: nil,
		},
	}
	/*
		if responseDomain.OrgGUID != "" {
			dr.Relationships.Organization.Data = RelationshipData{GUID: responseDomain.OrgGUID}
		}
	*/
	return dr
}

func ForDomainList(domainListRecords []repositories.DomainRecord, baseURL, requestURL url.URL) ListResponse {
	domainResponses := make([]interface{}, 0, len(domainListRecords))
	for _, domain := range domainListRecords {
		domainResponses = append(domainResponses, ForDomain(domain, baseURL))
	}

	return ForList(domainResponses, baseURL, requestURL)
}
