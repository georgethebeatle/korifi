package repositories_test

import (
	"context"
	"errors"
	"time"

	"code.cloudfoundry.org/korifi/api/config"
	apierrors "code.cloudfoundry.org/korifi/api/errors"
	"code.cloudfoundry.org/korifi/api/repositories"
	"code.cloudfoundry.org/korifi/api/repositories/fake"
	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"
	"code.cloudfoundry.org/korifi/tests/matchers"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("RoleRepository", func() {
	var (
		roleCreateMessage   repositories.CreateRoleMessage
		roleRepo            *repositories.RoleRepo
		cfOrg               *korifiv1alpha1.CFOrg
		createdRole         repositories.RoleRecord
		authorizedInChecker *fake.AuthorizedInChecker
		createErr           error
	)

	BeforeEach(func() {
		authorizedInChecker = new(fake.AuthorizedInChecker)
		roleMappings := map[string]config.Role{
			"space_developer":      {Name: spaceDeveloperRole.Name, Level: config.SpaceRole},
			"organization_manager": {Name: orgManagerRole.Name, Level: config.OrgRole, Propagate: true},
			"organization_user":    {Name: orgUserRole.Name, Level: config.OrgRole},
			"cf_user":              {Name: rootNamespaceUserRole.Name},
			"admin":                {Name: adminRole.Name, Propagate: true},
		}
		orgRepo := repositories.NewOrgRepo(rootNamespace, k8sClient, userClientFactory, nsPerms, time.Millisecond*2000)
		spaceRepo := repositories.NewSpaceRepo(namespaceRetriever, orgRepo, userClientFactory, nsPerms, time.Millisecond*2000)
		roleRepo = repositories.NewRoleRepo(
			userClientFactory,
			spaceRepo,
			authorizedInChecker,
			nsPerms,
			rootNamespace,
			roleMappings,
		)

		roleCreateMessage = repositories.CreateRoleMessage{}
		cfOrg = createOrgWithCleanup(ctx, uuid.NewString())
	})

	getTheRoleBinding := func(name, namespace string) rbacv1.RoleBinding {
		roleBinding := rbacv1.RoleBinding{}
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &roleBinding)).To(Succeed())

		return roleBinding
	}

	Describe("Create Org Role", func() {
		BeforeEach(func() {
			roleCreateMessage = repositories.CreateRoleMessage{
				GUID: uuid.NewString(),
				Type: "organization_manager",
				User: "myuser@example.com",
				Kind: rbacv1.UserKind,
				Org:  cfOrg.Name,
			}
		})

		JustBeforeEach(func() {
			createdRole, createErr = roleRepo.CreateRole(ctx, authInfo, roleCreateMessage)
		})

		When("the user doesn't have permissions to create roles", func() {
			It("fails", func() {
				Expect(createErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.ForbiddenError{}))
			})
		})

		When("the user is an admin", func() {
			var (
				expectedName       string
				cfUserExpectedName string
			)

			BeforeEach(func() {
				// Sha256 sum of "organization_manager::myuser@example.com"
				expectedName = "cf-172b9594a1f617258057870643bce8476179a4078845cb4d9d44171d7a8b648b"
				// Sha256 sum of "cf_user::myuser@example.com"
				cfUserExpectedName = "cf-156eb9a28b4143e61a5b43fb7e7a6b8de98495aa4b5da4ba871dc4eaa4c35433"
				createRoleBinding(ctx, userName, adminRole.Name, rootNamespace)
				createRoleBinding(ctx, userName, adminRole.Name, cfOrg.Name)
			})

			It("succeeds", func() {
				Expect(createErr).NotTo(HaveOccurred())
			})

			It("creates a role binding in the org namespace", func() {
				roleBinding := getTheRoleBinding(expectedName, cfOrg.Name)

				Expect(roleBinding.Labels).To(HaveKeyWithValue(repositories.RoleGuidLabel, roleCreateMessage.GUID))
				Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
				Expect(roleBinding.RoleRef.Name).To(Equal(orgManagerRole.Name))
				Expect(roleBinding.Subjects).To(HaveLen(1))
				Expect(roleBinding.Subjects[0].Kind).To(Equal(rbacv1.UserKind))
				Expect(roleBinding.Subjects[0].Name).To(Equal("myuser@example.com"))
			})

			It("creates a role binding for cf_user in the root namespace", func() {
				roleBinding := getTheRoleBinding(cfUserExpectedName, rootNamespace)

				Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
				Expect(roleBinding.RoleRef.Name).To(Equal(rootNamespaceUserRole.Name))
				Expect(roleBinding.Subjects).To(HaveLen(1))
				Expect(roleBinding.Subjects[0].Kind).To(Equal(rbacv1.UserKind))
				Expect(roleBinding.Subjects[0].Name).To(Equal("myuser@example.com"))
			})

			It("updated the create/updated timestamps", func() {
				Expect(time.Parse(time.RFC3339, createdRole.CreatedAt)).To(BeTemporally("~", time.Now(), 2*time.Second))
				Expect(time.Parse(time.RFC3339, createdRole.UpdatedAt)).To(BeTemporally("~", time.Now(), 2*time.Second))
				Expect(createdRole.CreatedAt).To(Equal(createdRole.UpdatedAt))
			})

			Describe("Role propagation", func() {
				When("the org role has propagation enabled", func() {
					BeforeEach(func() {
						roleCreateMessage.Type = "organization_manager"
					})

					It("enables the role binding propagation, but not for cf_user", func() {
						Expect(getTheRoleBinding(expectedName, cfOrg.Name).Annotations).To(HaveKeyWithValue(korifiv1alpha1.PropagateRoleBindingAnnotation, "true"))
						Expect(getTheRoleBinding(cfUserExpectedName, rootNamespace).Annotations).To(HaveKeyWithValue(korifiv1alpha1.PropagateRoleBindingAnnotation, "false"))
					})
				})

				When("the org role has propagation deactivated", func() {
					BeforeEach(func() {
						roleCreateMessage.Type = "organization_user"
						// Sha256 sum of "organization_user::myuser@example.com"
						expectedName = "cf-2a6f4cbdd1777d57b5b7b2ee835785dafa68c147719c10948397cfc2ea7246a3"
					})

					It("deactivates the role binding propagation", func() {
						Expect(createErr).NotTo(HaveOccurred())
						Expect(getTheRoleBinding(expectedName, cfOrg.Name).Annotations).To(HaveKeyWithValue(korifiv1alpha1.PropagateRoleBindingAnnotation, "false"))
						Expect(getTheRoleBinding(cfUserExpectedName, rootNamespace).Annotations).To(HaveKeyWithValue(korifiv1alpha1.PropagateRoleBindingAnnotation, "false"))
					})
				})
			})

			When("using a service account identity", func() {
				BeforeEach(func() {
					roleCreateMessage.Kind = rbacv1.ServiceAccountKind
					roleCreateMessage.User = "my-service-account"
					// Sha256 sum of "organization_manager::my-service-account"
					expectedName = "cf-6af123f3cf60cbba6c34bfa5f13314151ba309a9d7a9a19464aa052c773542e0"
				})

				It("succeeds and uses a service account subject kind", func() {
					Expect(createErr).NotTo(HaveOccurred())

					roleBinding := getTheRoleBinding(expectedName, cfOrg.Name)
					Expect(roleBinding.Subjects).To(HaveLen(1))
					Expect(roleBinding.Subjects[0].Name).To(Equal("my-service-account"))
					Expect(roleBinding.Subjects[0].Kind).To(Equal(rbacv1.ServiceAccountKind))
				})
			})

			When("the org does not exist", func() {
				BeforeEach(func() {
					roleCreateMessage.Org = "i-do-not-exist"
				})

				It("returns an error", func() {
					Expect(createErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.ForbiddenError{}))
				})
			})

			When("the role type is invalid", func() {
				BeforeEach(func() {
					roleCreateMessage.Type = "i-am-invalid"
				})

				It("returns an error", func() {
					Expect(createErr).To(MatchError(ContainSubstring("invalid role type")))
				})
			})

			When("the user is already bound to that role", func() {
				It("returns an unprocessable entity error", func() {
					anotherRoleCreateMessage := repositories.CreateRoleMessage{
						GUID: uuid.NewString(),
						Type: "organization_manager",
						User: "myuser@example.com",
						Kind: rbacv1.UserKind,
						Org:  roleCreateMessage.Org,
					}
					_, createErr = roleRepo.CreateRole(ctx, authInfo, anotherRoleCreateMessage)
					var apiErr apierrors.UnprocessableEntityError
					Expect(errors.As(createErr, &apiErr)).To(BeTrue())
					// Note: the cf cli expects this specific format and ignores the error if it matches it.
					Expect(apiErr.Detail()).To(Equal("User 'myuser@example.com' already has 'organization_manager' role"))
				})
			})
		})
	})

	Describe("Create Space Role", func() {
		var (
			cfSpace      *korifiv1alpha1.CFSpace
			expectedName string
		)

		BeforeEach(func() {
			// Sha256 sum of "space_developer::myuser@example.com"
			expectedName = "cf-94662df3659074e12fbb2a05fbda554db8fd0bf2f59394874412ebb0dddf6ba4"
			authorizedInChecker.AuthorizedInReturns(true, nil)
			cfSpace = createSpaceWithCleanup(ctx, cfOrg.Name, uuid.NewString())

			roleCreateMessage = repositories.CreateRoleMessage{
				GUID:  uuid.NewString(),
				Type:  "space_developer",
				User:  "myuser@example.com",
				Space: cfSpace.Name,
				Kind:  rbacv1.UserKind,
			}

			createRoleBinding(ctx, userName, adminRole.Name, rootNamespace)
			createRoleBinding(ctx, userName, adminRole.Name, cfOrg.Name)
			createRoleBinding(ctx, userName, adminRole.Name, cfSpace.Name)
		})

		JustBeforeEach(func() {
			Expect(k8sClient.Create(context.Background(), &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: cfOrg.Name,
				},
				Subjects: []rbacv1.Subject{
					{
						Kind: roleCreateMessage.Kind,
						Name: roleCreateMessage.User,
					},
				},
				RoleRef: rbacv1.RoleRef{
					Kind: "ClusterRole",
					Name: "org_user",
				},
			})).To(Succeed())

			createdRole, createErr = roleRepo.CreateRole(ctx, authInfo, roleCreateMessage)
		})

		It("succeeds", func() {
			Expect(createErr).NotTo(HaveOccurred())
		})

		It("creates a role binding in the space namespace", func() {
			roleBinding := getTheRoleBinding(expectedName, cfSpace.Name)

			Expect(roleBinding.Labels).To(HaveKeyWithValue(repositories.RoleGuidLabel, roleCreateMessage.GUID))
			Expect(roleBinding.RoleRef.Kind).To(Equal("ClusterRole"))
			Expect(roleBinding.RoleRef.Name).To(Equal(spaceDeveloperRole.Name))
			Expect(roleBinding.Subjects).To(HaveLen(1))
			Expect(roleBinding.Subjects[0].Kind).To(Equal(rbacv1.UserKind))
			Expect(roleBinding.Subjects[0].Name).To(Equal("myuser@example.com"))
		})

		It("verifies that the user has a role in the parent org", func() {
			Expect(authorizedInChecker.AuthorizedInCallCount()).To(Equal(1))
			_, userIdentity, org := authorizedInChecker.AuthorizedInArgsForCall(0)
			Expect(userIdentity.Name).To(Equal("myuser@example.com"))
			Expect(userIdentity.Kind).To(Equal(rbacv1.UserKind))
			Expect(org).To(Equal(cfOrg.Name))
		})

		It("updated the create/updated timestamps", func() {
			Expect(time.Parse(time.RFC3339, createdRole.CreatedAt)).To(BeTemporally("~", time.Now(), 2*time.Second))
			Expect(time.Parse(time.RFC3339, createdRole.UpdatedAt)).To(BeTemporally("~", time.Now(), 2*time.Second))
			Expect(createdRole.CreatedAt).To(Equal(createdRole.UpdatedAt))
		})

		When("using service accounts", func() {
			BeforeEach(func() {
				roleCreateMessage.Kind = rbacv1.ServiceAccountKind
				roleCreateMessage.User = "my-service-account"
			})

			It("sends the service account kind to the authorized in checker", func() {
				_, identity, _ := authorizedInChecker.AuthorizedInArgsForCall(0)
				Expect(identity.Kind).To(Equal(rbacv1.ServiceAccountKind))
				Expect(identity.Name).To(Equal("my-service-account"))
			})
		})

		When("checking an org role exists fails", func() {
			BeforeEach(func() {
				authorizedInChecker.AuthorizedInReturns(false, errors.New("boom!"))
			})

			It("returns an error", func() {
				Expect(createErr).To(MatchError(ContainSubstring("failed to check for role in parent org")))
			})
		})

		When("the space does not exist", func() {
			BeforeEach(func() {
				roleCreateMessage.Space = "i-do-not-exist"
			})

			It("returns an error", func() {
				Expect(createErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.UnprocessableEntityError{}))
			})
		})

		When("the space is forbidden", func() {
			BeforeEach(func() {
				anotherOrg := createOrgWithCleanup(ctx, uuid.NewString())
				cfSpace = createSpaceWithCleanup(ctx, anotherOrg.Name, uuid.NewString())
				roleCreateMessage.Space = cfSpace.Name
			})

			It("returns an error", func() {
				Expect(createErr).To(matchers.WrapErrorAssignableToTypeOf(apierrors.UnprocessableEntityError{}))
			})
		})

		When("the role type is invalid", func() {
			BeforeEach(func() {
				roleCreateMessage.Type = "i-am-invalid"
			})

			It("returns an error", func() {
				Expect(createErr).To(MatchError(ContainSubstring("invalid role type")))
			})
		})

		When("the user is already bound to that role", func() {
			It("returns an unprocessable entity error", func() {
				anotherRoleCreateMessage := repositories.CreateRoleMessage{
					GUID:  uuid.NewString(),
					Type:  "space_developer",
					User:  "myuser@example.com",
					Kind:  rbacv1.UserKind,
					Space: roleCreateMessage.Space,
				}
				_, createErr = roleRepo.CreateRole(ctx, authInfo, anotherRoleCreateMessage)
				Expect(createErr).To(SatisfyAll(
					BeAssignableToTypeOf(apierrors.UnprocessableEntityError{}),
					MatchError(ContainSubstring("already exists")),
				))
			})
		})

		When("the user does not have a role in the parent organization", func() {
			BeforeEach(func() {
				authorizedInChecker.AuthorizedInReturns(false, nil)
			})

			It("returns an unprocessable entity error", func() {
				Expect(createErr).To(SatisfyAll(
					BeAssignableToTypeOf(apierrors.UnprocessableEntityError{}),
					MatchError(ContainSubstring("no RoleBinding found")),
				))
			})
		})
	})

	Describe("list roles", func() {
		var (
			otherOrg            *korifiv1alpha1.CFOrg
			cfSpace, otherSpace *korifiv1alpha1.CFSpace
			roles               []repositories.RoleRecord
			listErr             error
		)

		BeforeEach(func() {
			otherOrg = createOrgWithCleanup(ctx, uuid.NewString())
			cfSpace = createSpaceWithCleanup(ctx, cfOrg.Name, uuid.NewString())
			otherSpace = createSpaceWithCleanup(ctx, cfOrg.Name, uuid.NewString())
			createRoleBinding(ctx, "my-user", orgUserRole.Name, cfOrg.Name)
			createRoleBinding(ctx, "my-user", spaceDeveloperRole.Name, cfSpace.Name)
			createRoleBinding(ctx, "my-user", spaceDeveloperRole.Name, otherSpace.Name)
			createRoleBinding(ctx, "my-user", orgUserRole.Name, otherOrg.Name)
		})

		JustBeforeEach(func() {
			roles, listErr = roleRepo.ListRoles(ctx, authInfo)
		})

		It("returns an empty list when user has no permissions to list roles", func() {
			Expect(listErr).NotTo(HaveOccurred())
			Expect(roles).To(BeEmpty())
		})

		When("the user has permission to list roles in some namespaces", func() {
			BeforeEach(func() {
				createRoleBinding(ctx, userName, orgUserRole.Name, cfOrg.Name)
				createRoleBinding(ctx, userName, spaceDeveloperRole.Name, cfSpace.Name)
			})

			It("returns the bindings in cfOrg and cfSpace only (for system user and my-user)", func() {
				Expect(listErr).NotTo(HaveOccurred())
				Expect(roles).To(ConsistOf(
					MatchFields(IgnoreExtras, Fields{
						"Kind":  Equal("User"),
						"User":  Equal("my-user"),
						"Type":  Equal("space_developer"),
						"Space": Equal(cfSpace.Name),
						"Org":   BeEmpty(),
					}),
					MatchFields(IgnoreExtras, Fields{
						"Kind":  Equal("User"),
						"User":  Equal("my-user"),
						"Type":  Equal("organization_user"),
						"Space": BeEmpty(),
						"Org":   Equal(cfOrg.Name),
					}),
					MatchFields(IgnoreExtras, Fields{
						"Kind":  Equal("User"),
						"User":  Equal(userName),
						"Type":  Equal("space_developer"),
						"Space": Equal(cfSpace.Name),
						"Org":   BeEmpty(),
					}),
					MatchFields(IgnoreExtras, Fields{
						"Kind":  Equal("User"),
						"User":  Equal(userName),
						"Type":  Equal("organization_user"),
						"Space": BeEmpty(),
						"Org":   Equal(cfOrg.Name),
					}),
				))
			})

			When("there are propagated role bindings", func() {
				BeforeEach(func() {
					createRoleBinding(ctx, "my-user", orgManagerRole.Name, cfSpace.Name, korifiv1alpha1.PropagatedFromLabel, "foo")
				})

				It("ignores them", func() {
					Expect(listErr).NotTo(HaveOccurred())
					Expect(roles).To(HaveLen(4))
				})
			})

			When("there are non-cf role bindings", func() {
				BeforeEach(func() {
					createRoleBinding(ctx, "my-user", "some-role", cfSpace.Name)
				})

				It("ignores them", func() {
					Expect(listErr).NotTo(HaveOccurred())
					Expect(roles).To(HaveLen(4))
				})
			})

			When("there are root namespace permissions", func() {
				BeforeEach(func() {
					createRoleBinding(ctx, userName, orgManagerRole.Name, rootNamespace)
				})

				It("ignores them", func() {
					Expect(listErr).NotTo(HaveOccurred())
					Expect(roles).To(HaveLen(4))
				})
			})
		})
	})
})
