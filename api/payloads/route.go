package payloads

import (
	"code.cloudfoundry.org/korifi/api/repositories"
)

type RouteCreate struct {
	Host          string             `json:"host" validate:"required"` // TODO: Not required when we support private domains
	Path          string             `json:"path"`
	Relationships RouteRelationships `json:"relationships" validate:"required"`
	Metadata      Metadata           `json:"metadata"`
}

type RouteRelationships struct {
	Domain Relationship `json:"domain" validate:"required"`
	Space  Relationship `json:"space" validate:"required"`
}

func (p RouteCreate) ToMessage(domainNamespace, domainName string) repositories.CreateRouteMessage {
	return repositories.CreateRouteMessage{
		Host:            p.Host,
		Path:            p.Path,
		SpaceGUID:       p.Relationships.Space.Data.GUID,
		DomainGUID:      p.Relationships.Domain.Data.GUID,
		DomainNamespace: domainNamespace,
		DomainName:      domainName,
		Labels:          p.Metadata.Labels,
		Annotations:     p.Metadata.Annotations,
	}
}

type RouteList struct {
	AppGUIDs    *string `schema:"app_guids"`
	SpaceGUIDs  *string `schema:"space_guids"`
	DomainGUIDs *string `schema:"domain_guids"`
	Hosts       *string `schema:"hosts"`
	Paths       *string `schema:"paths"`
}

func (p *RouteList) ToMessage() repositories.ListRoutesMessage {
	return repositories.ListRoutesMessage{
		AppGUIDs:    ParseArrayParam(p.AppGUIDs),
		SpaceGUIDs:  ParseArrayParam(p.SpaceGUIDs),
		DomainGUIDs: ParseArrayParam(p.DomainGUIDs),
		Hosts:       ParseArrayParam(p.Hosts),
		Paths:       ParseArrayParam(p.Paths),
	}
}

func (p *RouteList) SupportedKeys() []string {
	return []string{"app_guids", "space_guids", "domain_guids", "hosts", "paths"}
}

type RoutePatch struct {
	Metadata MetadataPatch `json:"metadata"`
}

func (a *RoutePatch) ToMessage(routeGUID, spaceGUID string) repositories.PatchRouteMetadataMessage {
	return repositories.PatchRouteMetadataMessage{
		RouteGUID: routeGUID,
		SpaceGUID: spaceGUID,
		MetadataPatch: repositories.MetadataPatch{
			Annotations: a.Metadata.Annotations,
			Labels:      a.Metadata.Labels,
		},
	}
}
