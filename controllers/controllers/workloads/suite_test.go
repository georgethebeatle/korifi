package workloads_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"
	"code.cloudfoundry.org/korifi/controllers/config"
	. "code.cloudfoundry.org/korifi/controllers/controllers/shared"
	. "code.cloudfoundry.org/korifi/controllers/controllers/workloads"
	"code.cloudfoundry.org/korifi/controllers/controllers/workloads/env"
	"code.cloudfoundry.org/korifi/controllers/controllers/workloads/labels"
	"code.cloudfoundry.org/korifi/controllers/controllers/workloads/testutils"
	"code.cloudfoundry.org/korifi/tools/k8s"
	admission "k8s.io/pod-security-admission/api"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	servicebindingv1beta1 "github.com/servicebinding/service-binding-controller/apis/v1beta1"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

var (
	ctx                 context.Context
	cancel              context.CancelFunc
	testEnv             *envtest.Environment
	k8sClient           client.Client
	cfRootNamespace     string
	cfOrg               *korifiv1alpha1.CFOrg
	imageRegistrySecret *corev1.Secret
)

const (
	packageRegistrySecretName = "test-package-registry-secret"
)

func TestWorkloadsControllers(t *testing.T) {
	SetDefaultEventuallyTimeout(10 * time.Second)
	SetDefaultEventuallyPollingInterval(250 * time.Millisecond)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Workloads Controllers Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.Level(zapcore.DebugLevel)))

	ctx, cancel = context.WithCancel(context.TODO())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "helm", "korifi", "controllers", "crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	Expect(korifiv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(servicebindingv1beta1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme.Scheme)).To(Succeed())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	webhookInstallOptions := &testEnv.WebhookInstallOptions
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:             scheme.Scheme,
		Host:               webhookInstallOptions.LocalServingHost,
		Port:               webhookInstallOptions.LocalServingPort,
		CertDir:            webhookInstallOptions.LocalServingCertDir,
		LeaderElection:     false,
		MetricsBindAddress: "0",
	})
	Expect(err).NotTo(HaveOccurred())

	cfRootNamespace = testutils.PrefixedGUID("root-namespace")

	controllerConfig := &config.ControllerConfig{
		CFProcessDefaults: config.CFProcessDefaults{
			MemoryMB:    500,
			DiskQuotaMB: 512,
		},
		CFRootNamespace:             cfRootNamespace,
		ContainerRegistrySecretName: packageRegistrySecretName,
		WorkloadsTLSSecretName:      "korifi-workloads-ingress-cert",
		WorkloadsTLSSecretNamespace: "korifi-controllers-system",
	}

	err = (NewCFAppReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		ctrl.Log.WithName("controllers").WithName("CFApp"),
		env.NewVCAPServicesEnvValueBuilder(k8sManager.GetClient()),
		env.NewVCAPApplicationEnvValueBuilder(k8sManager.GetClient()),
	)).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	registryAuthFetcherClient, err := k8sclient.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(registryAuthFetcherClient).NotTo(BeNil())
	cfBuildReconciler := NewCFBuildReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		ctrl.Log.WithName("controllers").WithName("CFBuild"),
		controllerConfig,
		env.NewWorkloadEnvBuilder(k8sManager.GetClient()),
	)
	err = (cfBuildReconciler).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (NewCFProcessReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		ctrl.Log.WithName("controllers").WithName("CFProcess"),
		controllerConfig,
		env.NewWorkloadEnvBuilder(k8sManager.GetClient()),
	)).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (NewCFPackageReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		ctrl.Log.WithName("controllers").WithName("CFPackage"),
	)).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	labelCompiler := labels.NewCompiler().Defaults(map[string]string{
		admission.EnforceLevelLabel: string(admission.LevelRestricted),
		admission.AuditLevelLabel:   string(admission.LevelRestricted),
	})

	err = NewCFOrgReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		ctrl.Log.WithName("controllers").WithName("CFOrg"),
		controllerConfig.ContainerRegistrySecretName,
		labelCompiler,
	).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = NewCFTaskReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		k8sManager.GetEventRecorderFor("cftask-controller"),
		ctrl.Log.WithName("controllers").WithName("CFTask"),
		env.NewWorkloadEnvBuilder(k8sManager.GetClient()),
		2*time.Second,
	).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = NewCFSpaceReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		ctrl.Log.WithName("controllers").WithName("CFSpace"),
		controllerConfig.ContainerRegistrySecretName,
		controllerConfig.CFRootNamespace,
		labelCompiler,
	).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	// Add new reconcilers here

	// Setup index for manager
	err = SetupIndexWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()

	createNamespace(cfRootNamespace)
	imageRegistrySecret = createSecret(ctx, k8sClient, packageRegistrySecretName, cfRootNamespace)
	cfOrg = createOrg(cfRootNamespace)
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

func createBuildWithDroplet(ctx context.Context, k8sClient client.Client, cfBuild *korifiv1alpha1.CFBuild, droplet *korifiv1alpha1.BuildDropletStatus) *korifiv1alpha1.CFBuild {
	Expect(
		k8sClient.Create(ctx, cfBuild),
	).To(Succeed())
	patchedCFBuild := cfBuild.DeepCopy()
	patchedCFBuild.Status.Conditions = []metav1.Condition{}
	patchedCFBuild.Status.Droplet = droplet
	Expect(
		k8sClient.Status().Patch(ctx, patchedCFBuild, client.MergeFrom(cfBuild)),
	).To(Succeed())
	return patchedCFBuild
}

func createNamespace(name string) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	Expect(
		k8sClient.Create(ctx, ns)).To(Succeed())
	return ns
}

func createSecret(ctx context.Context, k8sClient client.Client, name string, namespace string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		StringData: map[string]string{
			"foo": "bar",
		},
		Type: "Docker",
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	return secret
}

func createClusterRole(ctx context.Context, k8sClient client.Client, name string, rules []rbacv1.PolicyRule) *rbacv1.ClusterRole {
	role := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Rules: rules,
	}
	Expect(k8sClient.Create(ctx, role)).To(Succeed())
	return role
}

func createRoleBinding(ctx context.Context, k8sClient client.Client, roleBindingName, subjectName, roleReference, namespace string, annotations map[string]string) *rbacv1.RoleBinding {
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        roleBindingName,
			Namespace:   namespace,
			Annotations: annotations,
		},
		Subjects: []rbacv1.Subject{{
			Kind: rbacv1.UserKind,
			Name: subjectName,
		}},
		RoleRef: rbacv1.RoleRef{
			Kind: "ClusterRole",
			Name: roleReference,
		},
	}
	Expect(k8sClient.Create(ctx, roleBinding)).To(Succeed())
	return roleBinding
}

func createServiceAccount(ctx context.Context, k8sclient client.Client, serviceAccountName, namespace string, annotations map[string]string) *corev1.ServiceAccount {
	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        serviceAccountName,
			Namespace:   namespace,
			Annotations: annotations,
		},
		Secrets: []corev1.ObjectReference{
			{Name: serviceAccountName + "-token-someguid"},
			{Name: serviceAccountName + "-dockercfg-someguid"},
			{Name: packageRegistrySecretName},
		},
		ImagePullSecrets: []corev1.LocalObjectReference{
			{Name: serviceAccountName + "-dockercfg-someguid"},
			{Name: packageRegistrySecretName},
		},
	}
	Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())
	return serviceAccount
}

func patchAppWithDroplet(ctx context.Context, k8sClient client.Client, appGUID, spaceGUID, buildGUID string) *korifiv1alpha1.CFApp {
	cfApp := &korifiv1alpha1.CFApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appGUID,
			Namespace: spaceGUID,
		},
	}
	Expect(k8s.Patch(ctx, k8sClient, cfApp, func() {
		cfApp.Spec.CurrentDropletRef = corev1.LocalObjectReference{Name: buildGUID}
	})).To(Succeed())
	return cfApp
}

func createOrg(rootNamespace string) *korifiv1alpha1.CFOrg {
	org := &korifiv1alpha1.CFOrg{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testutils.PrefixedGUID("org"),
			Namespace: rootNamespace,
		},
		Spec: korifiv1alpha1.CFOrgSpec{
			DisplayName: testutils.PrefixedGUID("org"),
		},
	}
	Expect(k8sClient.Create(ctx, org)).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(org), org)).To(Succeed())
		g.Expect(meta.IsStatusConditionTrue(org.Status.Conditions, StatusConditionReady)).To(BeTrue())
	}).Should(Succeed())
	return org
}

func createSpace(org *korifiv1alpha1.CFOrg) *korifiv1alpha1.CFSpace {
	cfSpace := &korifiv1alpha1.CFSpace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testutils.PrefixedGUID("space"),
			Namespace: org.Status.GUID,
		},
		Spec: korifiv1alpha1.CFSpaceSpec{
			DisplayName: testutils.PrefixedGUID("space"),
		},
	}
	Expect(k8sClient.Create(ctx, cfSpace)).To(Succeed())
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cfSpace), cfSpace)).To(Succeed())
		g.Expect(meta.IsStatusConditionTrue(cfSpace.Status.Conditions, StatusConditionReady)).To(BeTrue())
	}).Should(Succeed())
	return cfSpace
}
