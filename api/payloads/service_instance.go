package payloads

import (
	"net/url"

	"code.cloudfoundry.org/korifi/api/repositories"
)

type ServiceInstanceCreate struct {
	Name          string                       `json:"name" validate:"required"`
	Type          string                       `json:"type" validate:"required,oneof=user-provided managed"`
	Tags          []string                     `json:"tags" validate:"serviceinstancetaglength"`
	Credentials   map[string]string            `json:"credentials"`
	Relationships ServiceInstanceRelationships `json:"relationships" validate:"required"`
	Metadata      Metadata                     `json:"metadata"`
}

type ServiceInstanceRelationships struct {
	Space       Relationship `json:"space" validate:"required"`
	ServicePlan Relationship `json:"service_plan" validate:"omitempty"`
}

func (p ServiceInstanceCreate) ToServiceInstanceCreateMessage() repositories.CreateServiceInstanceMessage {
	return repositories.CreateServiceInstanceMessage{
		Name:            p.Name,
		SpaceGUID:       p.Relationships.Space.Data.GUID,
		ServicePlanGUID: p.Relationships.ServicePlan.Data.GUID,
		Credentials:     p.Credentials,
		Type:            p.Type,
		Tags:            p.Tags,
		Labels:          p.Metadata.Labels,
		Annotations:     p.Metadata.Annotations,
	}
}

type ServiceInstanceList struct {
	Names      string
	SpaceGuids string
	OrderBy    string
}

func (l *ServiceInstanceList) ToMessage() repositories.ListServiceInstanceMessage {
	return repositories.ListServiceInstanceMessage{
		Names:      ParseArrayParam(l.Names),
		SpaceGuids: ParseArrayParam(l.SpaceGuids),
	}
}

func (l *ServiceInstanceList) SupportedKeys() []string {
	return []string{"names", "space_guids", "fields", "order_by", "per_page"}
}

func (l *ServiceInstanceList) DecodeFromURLValues(values url.Values) error {
	l.Names = values.Get("names")
	l.SpaceGuids = values.Get("space_guids")
	l.OrderBy = values.Get("order_by")
	return nil
}
