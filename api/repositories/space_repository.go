package repositories

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/labels"
	"time"

	"code.cloudfoundry.org/korifi/api/authorization"
	apierrors "code.cloudfoundry.org/korifi/api/errors"
	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"
	"code.cloudfoundry.org/korifi/tools/k8s"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	SpacePrefix       = "cf-space-"
	SpaceResourceType = "Space"
)

type CreateSpaceMessage struct {
	Name             string
	OrganizationGUID string
}

type ListSpacesMessage struct {
	Names             []string
	GUIDs             []string
	OrganizationGUIDs []string
}

type DeleteSpaceMessage struct {
	Name             string
	OrganizationGUID string
}

type PatchSpaceMetadataMessage struct {
	MetadataPatch
	GUID    string
	OrgGUID string
}

type SpaceRecord struct {
	DisplayName      string
	Name             string
	GUID             string
	OrganizationGUID string
	Labels           map[string]string
	Annotations      map[string]string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type SpaceRepo struct {
	orgRepo            *OrgRepo
	namespaceRetriever NamespaceRetriever
	userClientFactory  authorization.UserK8sClientFactory
	nsPerms            *authorization.NamespacePermissions
	timeout            time.Duration
}

func NewSpaceRepo(
	namespaceRetriever NamespaceRetriever,
	orgRepo *OrgRepo,
	userClientFactory authorization.UserK8sClientFactory,
	nsPerms *authorization.NamespacePermissions,
	timeout time.Duration,
) *SpaceRepo {
	return &SpaceRepo{
		orgRepo:            orgRepo,
		namespaceRetriever: namespaceRetriever,
		userClientFactory:  userClientFactory,
		nsPerms:            nsPerms,
		timeout:            timeout,
	}
}

func (r *SpaceRepo) CreateSpace(ctx context.Context, info authorization.Info, message CreateSpaceMessage) (SpaceRecord, error) {
	cfOrg, err := r.orgRepo.GetOrg(ctx, info, message.OrganizationGUID)
	if err != nil {
		return SpaceRecord{}, fmt.Errorf("failed to get parent organization: %w", err)
	}

	userClient, err := r.userClientFactory.BuildClient(info)
	if err != nil {
		return SpaceRecord{}, fmt.Errorf("failed to build user client: %w", err)
	}

	spaceCR, err := r.createSpaceCR(ctx, info, userClient, &korifiv1alpha1.CFSpace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SpacePrefix + uuid.NewString(),
			Namespace: cfOrg.Namespace,
		},
		Spec: korifiv1alpha1.CFSpaceSpec{
			DisplayName: message.Name,
		},
	})
	if err != nil {
		return SpaceRecord{}, apierrors.FromK8sError(err, SpaceResourceType)
	}

	return SpaceRecord{
		DisplayName:      spaceCR.Spec.DisplayName,
		Name:             spaceCR.Name,
		GUID:             spaceCR.Labels[korifiv1alpha1.CFSpaceGUIDLabelKey],
		OrganizationGUID: spaceCR.Labels[korifiv1alpha1.CFOrgGUIDLabelKey],
		CreatedAt:        spaceCR.CreationTimestamp.Time,
		UpdatedAt:        spaceCR.CreationTimestamp.Time,
	}, nil
}

//nolint:dupl
func (r *SpaceRepo) createSpaceCR(ctx context.Context,
	info authorization.Info,
	userClient client.WithWatch,
	space *korifiv1alpha1.CFSpace,
) (*korifiv1alpha1.CFSpace, error) {
	err := userClient.Create(ctx, space)
	if err != nil {
		return nil, fmt.Errorf("failed to create cf space: %w", apierrors.FromK8sError(err, SpaceResourceType))
	}

	timeoutCtx, cancelFn := context.WithTimeout(ctx, r.timeout)
	defer cancelFn()
	watch, err := userClient.Watch(timeoutCtx, &korifiv1alpha1.CFSpaceList{},
		client.InNamespace(space.Namespace),
		client.MatchingFields{"metadata.name": space.Name},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to set up watch on cf space: %w", apierrors.FromK8sError(err, SpaceResourceType))
	}

	conditionReady := false
	var createdSpace *korifiv1alpha1.CFSpace
	for res := range watch.ResultChan() {
		var ok bool
		createdSpace, ok = res.Object.(*korifiv1alpha1.CFSpace)
		if !ok {
			// should never happen, but avoids panic above
			continue
		}
		if meta.IsStatusConditionTrue(createdSpace.Status.Conditions, StatusConditionReady) {
			watch.Stop()
			conditionReady = true
			break
		}
	}

	if !conditionReady {
		return nil, fmt.Errorf("cf space did not get Condition `Ready`: 'True' within timeout period %d ms", r.timeout.Milliseconds())
	}

	return createdSpace, nil
}

func (r *SpaceRepo) ListSpaces(ctx context.Context, info authorization.Info, message ListSpacesMessage) ([]SpaceRecord, error) {
	userClient, err := r.userClientFactory.BuildClient(info)
	if err != nil {
		return []SpaceRecord{}, fmt.Errorf("failed to build user client: %w", err)
	}

	authorizedOrgNamespaces, err := r.nsPerms.GetAuthorizedOrgNamespaces(ctx, info)
	if err != nil {
		return nil, err
	}

	cfSpaces := []korifiv1alpha1.CFSpace{}

	for org := range authorizedOrgNamespaces {
		cfSpaceList := new(korifiv1alpha1.CFSpaceList)

		err = userClient.List(ctx, cfSpaceList, client.InNamespace(org))
		if k8serrors.IsForbidden(err) {
			continue
		}
		if err != nil {
			return nil, apierrors.FromK8sError(err, SpaceResourceType)
		}

		cfSpaces = append(cfSpaces, cfSpaceList.Items...)
	}

	authorizedSpaceNamespaces, err := r.nsPerms.GetAuthorizedSpaceNamespaces(ctx, info)
	if err != nil {
		return nil, err
	}

	var records []SpaceRecord
	for _, cfSpace := range cfSpaces {
		if !meta.IsStatusConditionTrue(cfSpace.Status.Conditions, StatusConditionReady) {
			continue
		}

		if !matchesFilter(cfSpace.Labels[korifiv1alpha1.CFOrgGUIDLabelKey], message.OrganizationGUIDs) {
			continue
		}

		if !matchesFilter(cfSpace.Labels[korifiv1alpha1.CFSpaceGUIDLabelKey], message.GUIDs) {
			continue
		}

		if !matchesFilter(cfSpace.Spec.DisplayName, message.Names) {
			continue
		}

		if !authorizedSpaceNamespaces[cfSpace.Name] {
			continue
		}

		records = append(records, cfSpaceToSpaceRecord(cfSpace))
	}

	return records, nil
}

func (r *SpaceRepo) GetSpace(ctx context.Context, info authorization.Info, spaceGUID string) (SpaceRecord, error) {
	/*
		ns, err := r.namespaceRetriever.NamespaceFor(ctx, spaceGUID, SpaceResourceType)
		if err != nil {
			return SpaceRecord{}, err
		}
	*/
	userClient, err := r.userClientFactory.BuildClient(info)
	if err != nil {
		return SpaceRecord{}, fmt.Errorf("get-space failed to build user client: %w", err)
	}

	cfSpaceList := korifiv1alpha1.CFSpaceList{}

	labelSelector, err := labels.ValidatedSelectorFromSet(map[string]string{
		korifiv1alpha1.CFSpaceGUIDLabelKey: spaceGUID,
	})

	labelOption := client.ListOptions{LabelSelector: labelSelector}
	err = userClient.List(ctx, &cfSpaceList, &labelOption)

	if err != nil {
		return SpaceRecord{}, fmt.Errorf("get-space failed to build user client: %w", err)
	}

	if len(cfSpaceList.Items) != 0 {
		if err != nil {
			return SpaceRecord{}, fmt.Errorf("more than 1 (or less) space returned for the space label")
		}
	}

	return cfSpaceToSpaceRecord(cfSpaceList.Items[0]), nil
}

func cfSpaceToSpaceRecord(cfSpace korifiv1alpha1.CFSpace) SpaceRecord {

	r := SpaceRecord{
		DisplayName:      cfSpace.Spec.DisplayName,
		Name:             cfSpace.Name,
		GUID:             cfSpace.Labels[korifiv1alpha1.CFSpaceGUIDLabelKey],
		OrganizationGUID: cfSpace.Namespace,
		Annotations:      cfSpace.Annotations,
		Labels:           cfSpace.Labels,
		CreatedAt:        cfSpace.CreationTimestamp.Time,
		UpdatedAt:        cfSpace.CreationTimestamp.Time,
	}

	delete(cfSpace.Labels, korifiv1alpha1.CFSpaceGUIDLabelKey)

	return r
}

func (r *SpaceRepo) DeleteSpace(ctx context.Context, info authorization.Info, message DeleteSpaceMessage) error {
	userClient, err := r.userClientFactory.BuildClient(info)
	if err != nil {
		return fmt.Errorf("failed to build user client: %w", err)
	}

	err = userClient.Delete(ctx, &korifiv1alpha1.CFSpace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      message.Name,
			Namespace: message.OrganizationGUID,
		},
	})

	return apierrors.FromK8sError(err, SpaceResourceType)
}

func (r *SpaceRepo) PatchSpaceMetadata(ctx context.Context, authInfo authorization.Info, message PatchSpaceMetadataMessage) (SpaceRecord, error) {
	userClient, err := r.userClientFactory.BuildClient(authInfo)
	if err != nil {
		return SpaceRecord{}, fmt.Errorf("failed to build user client: %w", err)
	}

	cfSpace := new(korifiv1alpha1.CFSpace)
	err = userClient.Get(ctx, client.ObjectKey{Namespace: message.OrgGUID, Name: message.GUID}, cfSpace)
	if err != nil {
		return SpaceRecord{}, fmt.Errorf("failed to get space: %w", apierrors.FromK8sError(err, SpaceResourceType))
	}

	err = k8s.PatchResource(ctx, userClient, cfSpace, func() {
		message.Apply(cfSpace)
	})
	if err != nil {
		return SpaceRecord{}, apierrors.FromK8sError(err, SpaceResourceType)
	}

	return cfSpaceToSpaceRecord(*cfSpace), nil
}
