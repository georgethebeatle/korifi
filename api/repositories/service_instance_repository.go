package repositories

import (
	"context"
	"fmt"

	"code.cloudfoundry.org/korifi/api/authorization"
	apierrors "code.cloudfoundry.org/korifi/api/errors"
	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	CFServiceInstanceGUIDLabel     = "korifi.cloudfoundry.org/service-instance-guid"
	ServiceInstanceResourceType    = "Service Instance"
	serviceBindingSecretTypePrefix = "servicebinding.io/"
)

type NamespaceGetter interface {
	GetNamespaceForServiceInstance(ctx context.Context, guid string) (string, error)
}

type ServiceInstanceRepo struct {
	namespaceRetriever   NamespaceRetriever
	userClientFactory    authorization.UserK8sClientFactory
	namespacePermissions *authorization.NamespacePermissions
}

func NewServiceInstanceRepo(
	namespaceRetriever NamespaceRetriever,
	userClientFactory authorization.UserK8sClientFactory,
	namespacePermissions *authorization.NamespacePermissions,
) *ServiceInstanceRepo {
	return &ServiceInstanceRepo{
		namespaceRetriever:   namespaceRetriever,
		userClientFactory:    userClientFactory,
		namespacePermissions: namespacePermissions,
	}
}

type CreateServiceInstanceMessage struct {
	Name            string
	SpaceGUID       string
	ServicePlanGUID string
	Credentials     map[string]string
	SysLogDrainUrl  string
	RouteServiceUrl string
	Type            string
	Tags            []string
	Labels          map[string]string
	Annotations     map[string]string
}

type ListServiceInstanceMessage struct {
	Names      []string
	SpaceGuids []string
	Labels     []string
}

type DeleteServiceInstanceMessage struct {
	GUID      string
	SpaceGUID string
}

type ServiceInstanceRecord struct {
	Name        string
	GUID        string
	SpaceGUID   string
	SecretName  string
	ServicePlan string
	Tags        []string
	Type        string
	CreatedAt   string
	UpdatedAt   string
}

func (r *ServiceInstanceRepo) CreateServiceInstance(ctx context.Context, authInfo authorization.Info, message CreateServiceInstanceMessage) (ServiceInstanceRecord, error) {
	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		// untested
		return ServiceInstanceRecord{}, fmt.Errorf("failed to build user client: %w", err)
	}

	cfServiceInstance := message.toCFServiceInstance()

	// Convert the Space GUID to a namespace
	ns, err := r.namespaceRetriever.NameFor(ctx, message.SpaceGUID, SpaceResourceType)
	if err != nil {
		return ServiceInstanceRecord{}, err
	}

	cfServiceInstance.Namespace = ns

	switch cfServiceInstance.Spec.Type {
	case korifiv1alpha1.UserProvidedType:
		break
	case korifiv1alpha1.ManagedType:
		cfServiceInstance.Spec.ServicePlan = message.ServicePlanGUID
	}

	err = userClient.Create(ctx, &cfServiceInstance)
	if err != nil {
		return ServiceInstanceRecord{}, apierrors.FromK8sError(err, ServiceInstanceResourceType)
	}

	secretObj := cfServiceInstanceToSecret(cfServiceInstance)
	_, err = controllerutil.CreateOrPatch(ctx, userClient, &secretObj, func() error {
		secretObj.StringData = message.Credentials
		if secretObj.StringData == nil {
			secretObj.StringData = map[string]string{}
		}

		switch cfServiceInstance.Spec.Type {
		case korifiv1alpha1.UserProvidedType:
			secretObj.Type = serviceBindingSecretTypePrefix + korifiv1alpha1.UserProvidedType
		case korifiv1alpha1.ManagedType:
			secretObj.Type = serviceBindingSecretTypePrefix + korifiv1alpha1.ManagedType
		}

		return nil
	})
	if err != nil {
		return ServiceInstanceRecord{}, apierrors.FromK8sError(err, ServiceInstanceResourceType)
	}

	return cfServiceInstanceToServiceInstanceRecord(cfServiceInstance), nil
}

// nolint:dupl
func (r *ServiceInstanceRepo) ListServiceInstances(ctx context.Context, authInfo authorization.Info, message ListServiceInstanceMessage) ([]ServiceInstanceRecord, error) {
	nsList, err := r.namespacePermissions.GetAuthorizedSpaceNamespaces(ctx, authInfo)
	if err != nil {
		// untested
		return nil, fmt.Errorf("failed to list namespaces for spaces with user role bindings: %w", err)
	}

	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return []ServiceInstanceRecord{}, fmt.Errorf("failed to build user client: %w", err)
	}

	var filteredServiceInstances []korifiv1alpha1.CFServiceInstance
	for ns := range nsList {
		serviceInstanceList := new(korifiv1alpha1.CFServiceInstanceList)
		err = userClient.List(ctx, serviceInstanceList, client.InNamespace(ns))
		if k8serrors.IsForbidden(err) {
			continue
		}
		if err != nil {
			return []ServiceInstanceRecord{}, fmt.Errorf("failed to list service instances in namespace %s: %w",
				ns,
				apierrors.FromK8sError(err, ServiceInstanceResourceType),
			)
		}
		filteredServiceInstances = append(filteredServiceInstances, applyServiceInstanceListFilter(serviceInstanceList.Items, message)...)
	}

	return returnServiceInstanceList(filteredServiceInstances), nil
}

func (r *ServiceInstanceRepo) GetServiceInstance(ctx context.Context, authInfo authorization.Info, guid string) (ServiceInstanceRecord, error) {
	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return ServiceInstanceRecord{}, fmt.Errorf("failed to build user client: %w", err)
	}

	namespace, err := r.namespaceRetriever.NamespaceFor(ctx, guid, ServiceInstanceResourceType)
	if err != nil {
		return ServiceInstanceRecord{}, fmt.Errorf("failed to get namespace for service instance: %w", err)
	}

	var serviceInstance korifiv1alpha1.CFServiceInstance
	if err := userClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: guid}, &serviceInstance); err != nil {
		return ServiceInstanceRecord{}, fmt.Errorf("failed to get service instance: %w", apierrors.FromK8sError(err, ServiceInstanceResourceType))
	}

	return cfServiceInstanceToServiceInstanceRecord(serviceInstance), nil
}

func (r *ServiceInstanceRepo) DeleteServiceInstance(ctx context.Context, authInfo authorization.Info, message DeleteServiceInstanceMessage) error {
	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return fmt.Errorf("failed to build user client: %w", err)
	}

	namespace, err := r.namespaceRetriever.NameFor(ctx, message.SpaceGUID, SpaceResourceType)
	if err != nil {
		return err
	}

	serviceInstance := &korifiv1alpha1.CFServiceInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      message.GUID,
			Namespace: namespace,
		},
	}

	if err := userClient.Delete(ctx, serviceInstance); err != nil {
		return fmt.Errorf("failed to delete service instance: %w", apierrors.FromK8sError(err, ServiceInstanceResourceType))
	}

	return nil
}

func (m CreateServiceInstanceMessage) toCFServiceInstance() korifiv1alpha1.CFServiceInstance {
	guid := uuid.NewString()
	return korifiv1alpha1.CFServiceInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name: guid,
			//Namespace:   m.SpaceGUID,
			Labels:      m.Labels,
			Annotations: m.Annotations,
		},
		Spec: korifiv1alpha1.CFServiceInstanceSpec{
			DisplayName: m.Name,
			SecretName:  guid,
			Type:        korifiv1alpha1.InstanceType(m.Type),
			Tags:        m.Tags,
		},
	}
}

func cfServiceInstanceToServiceInstanceRecord(cfServiceInstance korifiv1alpha1.CFServiceInstance) ServiceInstanceRecord {
	updatedAtTime, _ := getTimeLastUpdatedTimestamp(&cfServiceInstance.ObjectMeta)

	return ServiceInstanceRecord{
		Name:        cfServiceInstance.Spec.DisplayName,
		GUID:        cfServiceInstance.Name,
		SpaceGUID:   cfServiceInstance.Labels[korifiv1alpha1.CFSpaceGUIDLabelKey],
		SecretName:  cfServiceInstance.Spec.SecretName,
		ServicePlan: cfServiceInstance.Spec.ServicePlan,
		Tags:        cfServiceInstance.Spec.Tags,
		Type:        string(cfServiceInstance.Spec.Type),
		CreatedAt:   cfServiceInstance.CreationTimestamp.UTC().Format(TimestampFormat),
		UpdatedAt:   updatedAtTime,
	}
}

func cfServiceInstanceToSecret(cfServiceInstance korifiv1alpha1.CFServiceInstance) corev1.Secret {
	labels := make(map[string]string, 1)
	labels[CFServiceInstanceGUIDLabel] = cfServiceInstance.Name

	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfServiceInstance.Name,
			Namespace: cfServiceInstance.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: korifiv1alpha1.GroupVersion.String(),
					Kind:       "CFServiceInstance",
					Name:       cfServiceInstance.Name,
					UID:        cfServiceInstance.UID,
				},
			},
		},
	}
}

func applyServiceInstanceListFilter(serviceInstanceList []korifiv1alpha1.CFServiceInstance, message ListServiceInstanceMessage) []korifiv1alpha1.CFServiceInstance {
	if len(message.Names) == 0 && len(message.SpaceGuids) == 0 {
		return serviceInstanceList
	}

	var filtered []korifiv1alpha1.CFServiceInstance
	for _, serviceInstance := range serviceInstanceList {
		if matchesFilter(serviceInstance.Spec.DisplayName, message.Names) &&
			matchesFilter(serviceInstance.Labels[korifiv1alpha1.CFSpaceGUIDLabelKey], message.SpaceGuids) &&
			labelsFilters(serviceInstance.Labels, message.Labels) {
			filtered = append(filtered, serviceInstance)
		}
	}

	return filtered
}

func returnServiceInstanceList(serviceInstanceList []korifiv1alpha1.CFServiceInstance) []ServiceInstanceRecord {
	serviceInstanceRecords := make([]ServiceInstanceRecord, 0, len(serviceInstanceList))

	for _, serviceInstance := range serviceInstanceList {
		serviceInstanceRecords = append(serviceInstanceRecords, cfServiceInstanceToServiceInstanceRecord(serviceInstance))
	}
	return serviceInstanceRecords
}

/*
func updateSecretTypeFields(secret *corev1.Secret) {
	userSpecifiedType, typeSpecified := secret.StringData["type"]
	if typeSpecified {
		secret.Type = corev1.SecretType(serviceBindingSecretTypePrefix + userSpecifiedType)
	} else {
		secret.StringData["type"] = korifiv1alpha1.UserProvidedType
		secret.Type = serviceBindingSecretTypePrefix + korifiv1alpha1.UserProvidedType
	}
}
*/
