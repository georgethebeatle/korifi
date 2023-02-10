package presenter

import (
	"net/url"

	"code.cloudfoundry.org/korifi/api/repositories"
)

const (
	serviceCredentialBindingsBase = "/v3/service_credential_bindings"
)

type ServiceBindingResponse struct {
	GUID          string                              `json:"guid"`
	Type          string                              `json:"type"`
	Name          *string                             `json:"name"`
	CreatedAt     string                              `json:"created_at"`
	UpdatedAt     string                              `json:"updated_at"`
	LastOperation ServiceBindingLastOperationResponse `json:"last_operation"`
	Relationships Relationships                       `json:"relationships"`
	Links         ServiceBindingLinks                 `json:"links"`
	Metadata      Metadata                            `json:"metadata"`
}

type ServiceBindingLastOperationResponse struct {
	Type        string  `json:"type"`
	State       string  `json:"state"`
	Description *string `json:"description"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type ServiceBindingLinks struct {
	App             Link `json:"app"`
	ServiceInstance Link `json:"service_instance"`
	Self            Link `json:"self"`
	Details         Link `json:"details"`
}

func ForServiceBinding(record repositories.ServiceBindingRecord, baseURL url.URL) ServiceBindingResponse {
	return ServiceBindingResponse{
		GUID:      record.GUID,
		Type:      record.Type,
		Name:      record.Name,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
		LastOperation: ServiceBindingLastOperationResponse{
			Type:        record.LastOperation.Type,
			State:       record.LastOperation.State,
			Description: record.LastOperation.Description,
			CreatedAt:   record.LastOperation.CreatedAt,
			UpdatedAt:   record.LastOperation.UpdatedAt,
		},
		Relationships: map[string]Relationship{
			"app":              {&RelationshipData{record.AppGUID}},
			"service_instance": {&RelationshipData{record.ServiceInstanceGUID}},
		},
		Links: ServiceBindingLinks{
			App: Link{
				HRef: buildURL(baseURL).appendPath(appsBase, record.AppGUID).build(),
			},
			ServiceInstance: Link{
				HRef: buildURL(baseURL).appendPath(serviceInstancesBase, record.ServiceInstanceGUID).build(),
			},
			Self: Link{
				HRef: buildURL(baseURL).appendPath(serviceCredentialBindingsBase, record.GUID).build(),
			},
			Details: Link{
				HRef: buildURL(baseURL).appendPath(serviceCredentialBindingsBase, record.GUID, "details").build(),
			},
		},
		Metadata: Metadata{
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		},
	}
}

func ForServiceBindingList(serviceBindingRecord []repositories.ServiceBindingRecord, appRecords []repositories.AppRecord, serviceInstanceRecords []repositories.ServiceInstanceRecord, baseURL, requestURL url.URL) ListResponse {
	serviceBindingResponses := make([]interface{}, 0, len(serviceBindingRecord))
	for _, serviceBinding := range serviceBindingRecord {
		serviceBindingResponses = append(serviceBindingResponses, ForServiceBinding(serviceBinding, baseURL))
	}

	includedData := IncludedData{}

	ret := ForList(serviceBindingResponses, baseURL, requestURL)
	if len(appRecords) > 0 {
		for _, appRecord := range appRecords {
			includedData.Apps = append(includedData.Apps, ForApp(appRecord, baseURL))
		}
	}

	if len(serviceInstanceRecords) > 0 {
		for _, serviceInstanceRecord := range serviceInstanceRecords {
			includedData.ServiceInstances = append(includedData.ServiceInstances, ForServiceInstance(serviceInstanceRecord, baseURL))
		}
	}

	ret.Included = &includedData

	return ret
}
