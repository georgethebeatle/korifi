package payloads

import (
	"strings"

	"code.cloudfoundry.org/korifi/api/repositories"
)

type PackageCreate struct {
	Type          string                `json:"type" validate:"required,oneof='bits'"`
	Relationships *PackageRelationships `json:"relationships" validate:"required"`
	Metadata      Metadata              `json:"metadata"`
}

type PackageRelationships struct {
	App *Relationship `json:"app" validate:"required"`
}

func (m PackageCreate) ToMessage(record repositories.AppRecord) repositories.CreatePackageMessage {
	return repositories.CreatePackageMessage{
		Type:      m.Type,
		AppGUID:   record.GUID,
		SpaceGUID: record.SpaceGUID,
		Metadata: repositories.Metadata{
			Annotations: m.Metadata.Annotations,
			Labels:      m.Metadata.Labels,
		},
	}
}

type PackageUpdate struct {
	Metadata MetadataPatch `json:"metadata"`
}

func (u *PackageUpdate) ToMessage(packageGUID string) repositories.UpdatePackageMessage {
	return repositories.UpdatePackageMessage{
		GUID: packageGUID,
		MetadataPatch: repositories.MetadataPatch{
			Annotations: u.Metadata.Annotations,
			Labels:      u.Metadata.Labels,
		},
	}
}

type PackageListQueryParameters struct {
	AppGUIDs *string `schema:"app_guids"`
	States   *string `schema:"states"`
	OrderBy  string  `schema:"order_by"`

	// Below parameters are ignored, but must be included to ignore as query parameters
	PerPage string `schema:"per_page"`
}

func (p *PackageListQueryParameters) ToMessage() repositories.ListPackagesMessage {
	var descendingOrder bool

	if strings.HasPrefix(p.OrderBy, "-") {
		descendingOrder = true
	}

	return repositories.ListPackagesMessage{
		AppGUIDs:        ParseArrayParam(p.AppGUIDs),
		States:          ParseArrayParam(p.States),
		SortBy:          strings.TrimPrefix(p.OrderBy, "-"),
		DescendingOrder: descendingOrder,
	}
}

func (p *PackageListQueryParameters) SupportedKeys() []string {
	return []string{"app_guids", "order_by", "per_page", "states"}
}

type PackageListDropletsQueryParameters struct {
	// Below parameters are ignored, but must be included to ignore as query parameters
	States  string `schema:"states"`
	PerPage string `schema:"per_page"`
}

func (p *PackageListDropletsQueryParameters) ToMessage(packageGUIDs []string) repositories.ListDropletsMessage {
	return repositories.ListDropletsMessage{
		PackageGUIDs: packageGUIDs,
	}
}

func (p *PackageListDropletsQueryParameters) SupportedKeys() []string {
	return []string{"states", "per_page"}
}
