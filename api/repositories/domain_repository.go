package repositories

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"code.cloudfoundry.org/korifi/api/authorization"
	apierrors "code.cloudfoundry.org/korifi/api/errors"
	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"
	"code.cloudfoundry.org/korifi/tools/k8s"
	"github.com/google/uuid"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DomainResourceType = "Domain"
)

type DomainRepo struct {
	userClientFactory  authorization.UserK8sClientFactory
	namespaceRetriever NamespaceRetriever
	rootNamespace      string
}

func NewDomainRepo(
	userClientFactory authorization.UserK8sClientFactory,
	namespaceRetriever NamespaceRetriever,
	rootNamespace string,
) *DomainRepo {
	return &DomainRepo{
		userClientFactory:  userClientFactory,
		namespaceRetriever: namespaceRetriever,
		rootNamespace:      rootNamespace,
	}
}

type DomainRecord struct {
	Name        string
	GUID        string
	IsDefault   bool
	OrgGUID     string
	Labels      map[string]string
	Annotations map[string]string
	Namespace   string
	CreatedAt   string
	UpdatedAt   string
}

type CreateDomainMessage struct {
	Name     string
	OrgGUID  string
	Metadata Metadata
}

type UpdateDomainMessage struct {
	GUID          string
	MetadataPatch MetadataPatch
}

type ListDomainsMessage struct {
	Names []string
}

func (r *DomainRepo) GetDomain(ctx context.Context, authInfo authorization.Info, domainGUID string) (DomainRecord, error) {
	ns, err := r.namespaceRetriever.NamespaceFor(ctx, domainGUID, DomainResourceType)
	if err != nil {
		return DomainRecord{}, err
	}

	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return DomainRecord{}, fmt.Errorf("get-domain failed to create user client: %w", err)
	}

	domain := &korifiv1alpha1.CFDomain{}
	err = userClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: domainGUID}, domain)
	if err != nil {
		return DomainRecord{}, apierrors.NewForbiddenError(err, DomainResourceType)
	}

	return cfDomainToDomainRecord(domain), nil
}

func (r *DomainRepo) CreateDomain(ctx context.Context, authInfo authorization.Info, message CreateDomainMessage) (DomainRecord, error) {
	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return DomainRecord{}, fmt.Errorf("create-domain failed to create user client: %w", err)
	}

	cfDomain := &korifiv1alpha1.CFDomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:        uuid.NewString(),
			Namespace:   r.rootNamespace,
			Labels:      message.Metadata.Labels,
			Annotations: message.Metadata.Annotations,
		},
		Spec: korifiv1alpha1.CFDomainSpec{
			Name: message.Name,
		},
	}

	if message.OrgGUID != "" {
		if cfDomain.Labels == nil {
			cfDomain.Labels = map[string]string{}
		}
		cfDomain.Labels[korifiv1alpha1.CFOrgGUIDLabelKey] = message.OrgGUID
	}

	err = userClient.Create(ctx, cfDomain)
	if err != nil {
		return DomainRecord{}, fmt.Errorf("create-domain failed: %w", apierrors.FromK8sError(err, DomainResourceType))
	}

	return cfDomainToDomainRecord(cfDomain), nil
}

func (r *DomainRepo) UpdateDomain(ctx context.Context, authInfo authorization.Info, message UpdateDomainMessage) (DomainRecord, error) {
	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return DomainRecord{}, fmt.Errorf("create-domain failed to create user client: %w", err)
	}

	domain := &korifiv1alpha1.CFDomain{
		ObjectMeta: metav1.ObjectMeta{
			Name:      message.GUID,
			Namespace: r.rootNamespace,
		},
	}

	err = userClient.Get(ctx, client.ObjectKeyFromObject(domain), domain)
	if err != nil {
		return DomainRecord{}, fmt.Errorf("update-domain failed: %w", apierrors.FromK8sError(err, DomainResourceType))
	}

	err = k8s.PatchResource(ctx, userClient, domain, func() {
		message.MetadataPatch.Apply(domain)
	})
	if err != nil {
		return DomainRecord{}, fmt.Errorf("failed to patch domain metadata: %w", apierrors.FromK8sError(err, DomainResourceType))
	}

	return cfDomainToDomainRecord(domain), nil
}

func (r *DomainRepo) ListDomains(ctx context.Context, authInfo authorization.Info, message ListDomainsMessage) ([]DomainRecord, error) {
	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return []DomainRecord{}, fmt.Errorf("list-domain failed to create user client: %w", err)
	}

	cfdomainList := &korifiv1alpha1.CFDomainList{}
	err = userClient.List(ctx, cfdomainList, client.InNamespace(r.rootNamespace))
	if err != nil {
		if k8serrors.IsForbidden(err) {
			return []DomainRecord{}, nil
		}
		// untested
		return []DomainRecord{}, fmt.Errorf("failed to list domains in namespace %s: %w", r.rootNamespace, apierrors.FromK8sError(err, DomainResourceType))
	}

	filtered := applyDomainListFilterAndOrder(cfdomainList.Items, message)

	return returnDomainList(filtered), nil
}

func (r *DomainRepo) GetDomainByName(ctx context.Context, authInfo authorization.Info, domainName string) (DomainRecord, error) {
	domainRecords, err := r.ListDomains(ctx, authInfo, ListDomainsMessage{
		Names: []string{domainName},
	})
	if err != nil {
		return DomainRecord{}, err
	}

	if len(domainRecords) == 0 {
		return DomainRecord{}, apierrors.NewNotFoundError(fmt.Errorf("domain %q not found", domainName), DomainResourceType)
	}

	return domainRecords[0], nil
}

func (r *DomainRepo) DeleteDomain(ctx context.Context, authInfo authorization.Info, domainGUID string) error {
	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return fmt.Errorf("delete-domain failed to create user client: %w", err)
	}

	cfDomain := &korifiv1alpha1.CFDomain{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.rootNamespace,
			Name:      domainGUID,
		},
	}

	err = userClient.Delete(ctx, cfDomain)
	if err != nil {
		return apierrors.FromK8sError(err, DomainResourceType)
	}

	return nil
}

func applyDomainListFilterAndOrder(domainList []korifiv1alpha1.CFDomain, message ListDomainsMessage) []korifiv1alpha1.CFDomain {
	var filtered []korifiv1alpha1.CFDomain
	if len(message.Names) > 0 {
		for _, domain := range domainList {
			for _, name := range message.Names {
				if domain.Spec.Name == name {
					filtered = append(filtered, domain)
				}
			}
		}
	} else {
		filtered = domainList
	}

	// TODO: use the future message.Order fields to reorder the list of results
	// For now, we order by created_at by default- if you really want to optimize runtime you can use bucketsort
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreationTimestamp.Before(&filtered[j].CreationTimestamp)
	})

	return filtered
}

func returnDomainList(domainList []korifiv1alpha1.CFDomain) []DomainRecord {
	domainRecords := make([]DomainRecord, 0, len(domainList))

	for i := range domainList {
		domainRecords = append(domainRecords, cfDomainToDomainRecord(&domainList[i]))
	}
	return domainRecords
}

func cfDomainToDomainRecord(cfDomain *korifiv1alpha1.CFDomain) DomainRecord {
	updatedAtTime, _ := getTimeLastUpdatedTimestamp(&cfDomain.ObjectMeta)

	isDefaultDomain := cfDomain.Labels[korifiv1alpha1.CFDefaultDomainLabelKey]

	r := DomainRecord{
		Name:        cfDomain.Spec.Name,
		GUID:        cfDomain.Name,
		IsDefault:   isDefaultDomain == "true",
		OrgGUID:     cfDomain.Labels[korifiv1alpha1.CFOrgGUIDLabelKey],
		Namespace:   cfDomain.Namespace,
		CreatedAt:   cfDomain.CreationTimestamp.UTC().Format(TimestampFormat),
		UpdatedAt:   updatedAtTime,
		Labels:      cfDomain.Labels,
		Annotations: cfDomain.Annotations,
	}

	for label, _ := range r.Labels {
		if strings.Contains(label, "korifi.cloudfoundry.org") {
			delete(r.Labels, label)
		}
	}

	for annotation, _ := range r.Annotations {
		if strings.Contains(annotation, "kubernetes.io") {
			delete(r.Annotations, annotation)
		}
	}

	return r
}
