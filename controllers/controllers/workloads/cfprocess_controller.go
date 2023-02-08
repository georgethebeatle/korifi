/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workloads

import (
	korifiv1alpha1 "code.cloudfoundry.org/korifi/controllers/api/v1alpha1"
	"code.cloudfoundry.org/korifi/controllers/config"
	"code.cloudfoundry.org/korifi/controllers/controllers/shared"
	"code.cloudfoundry.org/korifi/tools/k8s"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"regexp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sort"
	"strconv"
)

type EnvBuilder interface {
	BuildEnv(ctx context.Context, cfApp *korifiv1alpha1.CFApp) ([]corev1.EnvVar, error)
}

// CFProcessReconciler reconciles a CFProcess object
type CFProcessReconciler struct {
	k8sClient        client.Client
	scheme           *runtime.Scheme
	log              logr.Logger
	controllerConfig *config.ControllerConfig
	envBuilder       EnvBuilder
}

type vCapApplicationType struct {
	ApplicationId   string   `json:"application_id"`
	ApplicationName string   `json:"application_name"`
	ApplicationUris []string `json:"application_uris"`
	CfApi           string   `json:"cf_api"`
	Limits          struct {
		Fds int `json:"fds"`
	} `json:"limits"`
	Name             string      `json:"name"`
	OrganizationId   string      `json:"organization_id"`
	OrganizationName string      `json:"organization_name"`
	SpaceId          string      `json:"space_id"`
	SpaceName        string      `json:"space_name"`
	Uris             []string    `json:"uris"`
	Users            interface{} `json:"users"`
}

func NewCFProcessReconciler(
	client client.Client,
	scheme *runtime.Scheme,
	log logr.Logger,
	controllerConfig *config.ControllerConfig,
	envBuilder EnvBuilder,
) *k8s.PatchingReconciler[korifiv1alpha1.CFProcess, *korifiv1alpha1.CFProcess] {
	processReconciler := CFProcessReconciler{k8sClient: client, scheme: scheme, log: log, controllerConfig: controllerConfig, envBuilder: envBuilder}
	return k8s.NewPatchingReconciler[korifiv1alpha1.CFProcess, *korifiv1alpha1.CFProcess](log, client, &processReconciler)
}

//+kubebuilder:rbac:groups=korifi.cloudfoundry.org,resources=cfprocesses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=korifi.cloudfoundry.org,resources=cfprocesses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=korifi.cloudfoundry.org,resources=cfprocesses/finalizers,verbs=update
//+kubebuilder:rbac:groups=korifi.cloudfoundry.org,resources=appworkloads,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;patch

func (r *CFProcessReconciler) ReconcileResource(ctx context.Context, cfProcess *korifiv1alpha1.CFProcess) (ctrl.Result, error) {
	cfApp := new(korifiv1alpha1.CFApp)
	err := r.k8sClient.Get(ctx, types.NamespacedName{Name: cfProcess.Spec.AppRef.Name, Namespace: cfProcess.Namespace}, cfApp)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("Error when trying to fetch CFApp %s/%s", cfProcess.Namespace, cfProcess.Spec.AppRef.Name))
		return ctrl.Result{}, err
	}

	if cfProcess.Labels == nil {
		cfProcess.Labels = map[string]string{}
	}
	//TODO: Find a way better way to do this!
	cfProcess.Labels[korifiv1alpha1.CFSpaceGUIDLabelKey] = regexp.MustCompile(`.*([a-fA-F0-9]{8}-[a-fA-F0-9]{4}-4[a-fA-F0-9]{3}-[8|9|aA|bB][a-fA-F0-9]{3}-[a-fA-F0-9]{12})$`).ReplaceAllString(cfProcess.Namespace, "$1")

	err = controllerutil.SetControllerReference(cfApp, cfProcess, r.scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	cfAppRev := korifiv1alpha1.CFAppRevisionKeyDefault
	if foundValue, ok := cfApp.GetAnnotations()[korifiv1alpha1.CFAppRevisionKey]; ok {
		cfAppRev = foundValue
	}

	if needsAppWorkload(cfApp, cfProcess) {
		err = r.createOrPatchAppWorkload(ctx, cfApp, cfProcess, cfAppRev)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	err = r.cleanUpAppWorkloads(ctx, cfProcess, cfApp.Spec.DesiredState, cfAppRev)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func needsAppWorkload(cfApp *korifiv1alpha1.CFApp, cfProcess *korifiv1alpha1.CFProcess) bool {
	if cfApp.Spec.DesiredState != korifiv1alpha1.StartedState {
		return false
	}

	// note that the defaulting webhook ensures DesiredInstances is never nil
	return cfProcess.Spec.DesiredInstances != nil && *cfProcess.Spec.DesiredInstances > 0
}

func (r *CFProcessReconciler) createOrPatchAppWorkload(ctx context.Context, cfApp *korifiv1alpha1.CFApp, cfProcess *korifiv1alpha1.CFProcess, cfAppRev string) error {
	cfBuild := new(korifiv1alpha1.CFBuild)
	err := r.k8sClient.Get(ctx, types.NamespacedName{Name: cfApp.Spec.CurrentDropletRef.Name, Namespace: cfProcess.Namespace}, cfBuild)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("Error when trying to fetch CFBuild %s/%s", cfProcess.Namespace, cfApp.Spec.CurrentDropletRef.Name))
		return err
	}

	if cfBuild.Status.Droplet == nil {
		r.log.Error(err, fmt.Sprintf("No build droplet status on CFBuild %s/%s", cfProcess.Namespace, cfApp.Spec.CurrentDropletRef.Name))
		return errors.New("no build droplet status on CFBuild")
	}

	var appPort int
	appPort, err = r.getPort(ctx, cfProcess, cfApp)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("Error when trying to fetch routes for CFApp %s/%s", cfProcess.Namespace, cfApp.Spec.DisplayName))
		return err
	}

	envVars, err := r.envBuilder.BuildEnv(ctx, cfApp)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("error when trying build the process environment for app: %s/%s", cfProcess.Namespace, cfApp.Spec.DisplayName))
		return err
	}

	actualAppWorkload := &korifiv1alpha1.AppWorkload{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cfProcess.Namespace,
			Name:      generateAppWorkloadName(cfAppRev, cfProcess.Name),
		},
	}

	// Locate the space the Application was created in
	cfSpaceList := korifiv1alpha1.CFSpaceList{}
	labelSelector, err := labels.ValidatedSelectorFromSet(map[string]string{
		korifiv1alpha1.CFSpaceGUIDLabelKey: cfApp.Labels[korifiv1alpha1.CFSpaceGUIDLabelKey],
	})

	if err != nil {
		return err
	}

	err = r.k8sClient.List(ctx, &cfSpaceList, &client.ListOptions{LabelSelector: labelSelector})

	if err != nil || len(cfSpaceList.Items) != 1 {
		r.log.Error(err, "Error when initializing AppWorkload: Unable to locate space")
		return err
	}

	cfSpace := cfSpaceList.Items[0]

	cfOrgList := korifiv1alpha1.CFOrgList{}
	labelSelector, err = labels.ValidatedSelectorFromSet(map[string]string{
		korifiv1alpha1.CFOrgGUIDLabelKey: cfSpace.Labels[korifiv1alpha1.CFOrgGUIDLabelKey],
	})

	if err != nil {
		return err
	}

	err = r.k8sClient.List(ctx, &cfOrgList, &client.ListOptions{LabelSelector: labelSelector})

	if err != nil || len(cfOrgList.Items) != 1 {
		r.log.Error(err, "Error when initializing AppWorkload: Unable to locate org")
		return err
	}

	cfOrg := cfOrgList.Items[0]

	vcapApplication := vCapApplicationType{
		ApplicationId:    cfApp.Name,
		ApplicationName:  cfApp.Spec.DisplayName,
		CfApi:            r.controllerConfig.ApiServerURL,
		OrganizationId:   cfOrg.Labels[korifiv1alpha1.CFOrgGUIDLabelKey],
		OrganizationName: cfOrg.Spec.DisplayName,
		SpaceId:          cfSpace.Labels[korifiv1alpha1.CFSpaceGUIDLabelKey],
		SpaceName:        cfSpace.Spec.DisplayName,
	}

	var cfRoutesForProcess korifiv1alpha1.CFRouteList
	err = r.k8sClient.List(ctx, &cfRoutesForProcess, client.InNamespace(cfApp.GetNamespace()), client.MatchingFields{shared.IndexRouteDestinationAppName: cfApp.Name})
	if err != nil {
		return err
	}

	for _, cfRoute := range cfRoutesForProcess.Items {
		vcapApplication.ApplicationUris = append(vcapApplication.ApplicationUris, config.DefaultExternalProtocol+"://"+cfRoute.Status.URI)
	}

	var desiredAppWorkload *korifiv1alpha1.AppWorkload
	desiredAppWorkload, err = r.generateAppWorkload(actualAppWorkload, cfApp, cfProcess, cfBuild, vcapApplication, appPort, envVars)
	if err != nil { // untested
		r.log.Error(err, "Error when initializing AppWorkload")
		return err
	}

	_, err = controllerutil.CreateOrPatch(ctx, r.k8sClient, actualAppWorkload, appWorkloadMutateFunction(actualAppWorkload, desiredAppWorkload))
	if err != nil {
		r.log.Error(err, "Error calling CreateOrPatch on AppWorkload")
		return err
	}
	return nil
}

func (r *CFProcessReconciler) cleanUpAppWorkloads(ctx context.Context, cfProcess *korifiv1alpha1.CFProcess, desiredState korifiv1alpha1.DesiredState, cfAppRev string) error {
	appWorkloadsForProcess, err := r.fetchAppWorkloadsForProcess(ctx, cfProcess)
	if err != nil {
		r.log.Error(err, fmt.Sprintf("Error when trying to fetch AppWorkloads for Process %s/%s", cfProcess.Namespace, cfProcess.Name))
		return err
	}

	for i, currentAppWorkload := range appWorkloadsForProcess {
		if needsToDeleteAppWorkload(desiredState, cfProcess, currentAppWorkload, cfAppRev) {
			err := r.k8sClient.Delete(ctx, &appWorkloadsForProcess[i])
			if err != nil {
				r.log.Info(fmt.Sprintf("Error occurred deleting AppWorkload: %s, %s", currentAppWorkload.Name, err))
				return err
			}
		}
	}
	return nil
}

func needsToDeleteAppWorkload(
	desiredState korifiv1alpha1.DesiredState,
	cfProcess *korifiv1alpha1.CFProcess,
	appWorkload korifiv1alpha1.AppWorkload,
	cfAppRev string,
) bool {
	return desiredState == korifiv1alpha1.StoppedState ||
		(cfProcess.Spec.DesiredInstances != nil && *cfProcess.Spec.DesiredInstances == 0) ||
		appWorkload.Labels[korifiv1alpha1.CFAppRevisionKey] != cfAppRev
}

func appWorkloadMutateFunction(actualAppWorkload, desiredAppWorkload *korifiv1alpha1.AppWorkload) controllerutil.MutateFn {
	return func() error {
		actualAppWorkload.Labels = desiredAppWorkload.Labels
		actualAppWorkload.Annotations = desiredAppWorkload.Annotations
		actualAppWorkload.OwnerReferences = desiredAppWorkload.OwnerReferences
		actualAppWorkload.Spec = desiredAppWorkload.Spec
		return nil
	}
}

func (r *CFProcessReconciler) generateAppWorkload(actualAppWorkload *korifiv1alpha1.AppWorkload, cfApp *korifiv1alpha1.CFApp, cfProcess *korifiv1alpha1.CFProcess, cfBuild *korifiv1alpha1.CFBuild, vcapApplication vCapApplicationType, appPort int, envVars []corev1.EnvVar) (*korifiv1alpha1.AppWorkload, error) {
	var desiredAppWorkload korifiv1alpha1.AppWorkload
	actualAppWorkload.DeepCopyInto(&desiredAppWorkload)

	envVars = generateEnvVars(appPort, envVars)
	envVars = gernateVcapApplication(vcapApplication, envVars)

	desiredAppWorkload.Labels = make(map[string]string)
	desiredAppWorkload.Labels[korifiv1alpha1.CFAppGUIDLabelKey] = cfApp.Name
	cfAppRevisionKeyValue := korifiv1alpha1.CFAppRevisionKeyDefault
	if cfApp.Annotations != nil {
		if foundValue, has := cfApp.Annotations[korifiv1alpha1.CFAppRevisionKey]; has {
			cfAppRevisionKeyValue = foundValue
		}
	}
	desiredAppWorkload.Labels[korifiv1alpha1.CFAppRevisionKey] = cfAppRevisionKeyValue
	desiredAppWorkload.Labels[korifiv1alpha1.CFProcessGUIDLabelKey] = cfProcess.Name
	desiredAppWorkload.Labels[korifiv1alpha1.CFProcessTypeLabelKey] = cfProcess.Spec.ProcessType

	desiredAppWorkload.Spec.GUID = cfProcess.Name
	desiredAppWorkload.Spec.Version = cfAppRevisionKeyValue
	desiredAppWorkload.Spec.Resources.Requests = corev1.ResourceList{
		corev1.ResourceCPU:              calculateCPURequest(cfProcess.Spec.MemoryMB),
		corev1.ResourceEphemeralStorage: mebibyteQuantity(cfProcess.Spec.DiskQuotaMB),
		corev1.ResourceMemory:           mebibyteQuantity(cfProcess.Spec.MemoryMB),
	}
	desiredAppWorkload.Spec.Resources.Limits = corev1.ResourceList{
		corev1.ResourceEphemeralStorage: mebibyteQuantity(cfProcess.Spec.DiskQuotaMB),
		corev1.ResourceMemory:           mebibyteQuantity(cfProcess.Spec.MemoryMB),
	}
	desiredAppWorkload.Spec.ProcessType = cfProcess.Spec.ProcessType
	desiredAppWorkload.Spec.Command = commandForProcess(cfProcess, cfApp)
	desiredAppWorkload.Spec.AppGUID = cfApp.Name
	desiredAppWorkload.Spec.Image = cfBuild.Status.Droplet.Registry.Image
	desiredAppWorkload.Spec.ImagePullSecrets = cfBuild.Status.Droplet.Registry.ImagePullSecrets
	desiredAppWorkload.Spec.Ports = cfProcess.Spec.Ports
	if cfProcess.Spec.DesiredInstances != nil {
		desiredAppWorkload.Spec.Instances = int32(*cfProcess.Spec.DesiredInstances)
	}
	desiredAppWorkload.Spec.Env = envVars
	desiredAppWorkload.Spec.StartupProbe = startupProbe(cfProcess, appPort)
	desiredAppWorkload.Spec.LivenessProbe = livenessProbe(cfProcess, appPort)
	desiredAppWorkload.Spec.RunnerName = r.controllerConfig.RunnerName

	err := controllerutil.SetControllerReference(cfProcess, &desiredAppWorkload, r.scheme)
	if err != nil {
		return nil, err
	}

	return &desiredAppWorkload, err
}

func calculateCPURequest(memoryMiB int64) resource.Quantity {
	const (
		cpuRequestRatio         int64 = 1024
		cpuRequestMinMillicores int64 = 5
	)
	cpuMillicores := int64(100) * memoryMiB / cpuRequestRatio
	if cpuMillicores < cpuRequestMinMillicores {
		cpuMillicores = cpuRequestMinMillicores
	}
	return *resource.NewScaledQuantity(cpuMillicores, resource.Milli)
}

func generateAppWorkloadName(cfAppRev string, processGUID string) string {
	h := sha1.New()
	h.Write([]byte(cfAppRev))
	appRevHash := h.Sum(nil)
	appWorkloadName := processGUID + fmt.Sprintf("-%x", appRevHash)[:5]
	return appWorkloadName
}

func (r *CFProcessReconciler) fetchAppWorkloadsForProcess(ctx context.Context, cfProcess *korifiv1alpha1.CFProcess) ([]korifiv1alpha1.AppWorkload, error) {
	allAppWorkloads := &korifiv1alpha1.AppWorkloadList{}
	err := r.k8sClient.List(ctx, allAppWorkloads, client.InNamespace(cfProcess.Namespace))
	if err != nil {
		return []korifiv1alpha1.AppWorkload{}, err
	}
	var appWorkloadsForProcess []korifiv1alpha1.AppWorkload
	for _, currentAppWorkload := range allAppWorkloads.Items {
		if processGUID, has := currentAppWorkload.Labels[korifiv1alpha1.CFProcessGUIDLabelKey]; has && processGUID == cfProcess.Name {
			appWorkloadsForProcess = append(appWorkloadsForProcess, currentAppWorkload)
		}
	}
	return appWorkloadsForProcess, err
}

func (r *CFProcessReconciler) getPort(ctx context.Context, cfProcess *korifiv1alpha1.CFProcess, cfApp *korifiv1alpha1.CFApp) (int, error) {
	// Get Routes for the process
	var cfRoutesForProcess korifiv1alpha1.CFRouteList
	err := r.k8sClient.List(ctx, &cfRoutesForProcess, client.InNamespace(cfApp.GetNamespace()), client.MatchingFields{shared.IndexRouteDestinationAppName: cfApp.Name})
	if err != nil {
		return 0, err
	}

	// In case there are multiple routes, prefer the oldest one
	sort.Slice(cfRoutesForProcess.Items, func(i, j int) bool {
		return cfRoutesForProcess.Items[i].CreationTimestamp.Before(&cfRoutesForProcess.Items[j].CreationTimestamp)
	})

	// Filter those destinations
	for _, cfRoute := range cfRoutesForProcess.Items {
		for _, destination := range cfRoute.Status.Destinations {
			if destination.AppRef.Name == cfApp.Name && destination.ProcessType == cfProcess.Spec.ProcessType && destination.Port != 0 {
				// Just use the first candidate port
				return destination.Port, nil
			}
		}
	}

	return 8080, nil
}

func gernateVcapApplication(vcapApplication vCapApplicationType, commonEnv []corev1.EnvVar) []corev1.EnvVar {
	var result []corev1.EnvVar
	result = append(result, commonEnv...)

	data, _ := json.Marshal(vcapApplication)

	result = append(result,
		corev1.EnvVar{Name: "VCAP_APPLICATION", Value: string(data)},
	)

	return result
}

func generateEnvVars(port int, commonEnv []corev1.EnvVar) []corev1.EnvVar {
	var result []corev1.EnvVar
	result = append(result, commonEnv...)
	portString := strconv.Itoa(port)

	result = append(result,

		corev1.EnvVar{Name: "VCAP_APP_HOST", Value: "0.0.0.0"},
		corev1.EnvVar{Name: "VCAP_APP_PORT", Value: portString},
		corev1.EnvVar{Name: "PORT", Value: portString},
	)

	return result
}

func commandForProcess(process *korifiv1alpha1.CFProcess, app *korifiv1alpha1.CFApp) []string {
	cmd := process.Spec.Command
	if cmd == "" {
		cmd = process.Spec.DetectedCommand
	}
	if cmd == "" {
		return []string{}
	}
	if app.Spec.Lifecycle.Type == korifiv1alpha1.BuildpackLifecycle {
		return []string{"/cnb/lifecycle/launcher", cmd}
	}
	return []string{"/bin/sh", "-c", cmd}
}

func makeProbeHandler(cfProcess *korifiv1alpha1.CFProcess, port int) corev1.ProbeHandler {
	var probeHandler corev1.ProbeHandler

	switch cfProcess.Spec.HealthCheck.Type {
	case korifiv1alpha1.HTTPHealthCheckType:
		probeHandler.HTTPGet = &corev1.HTTPGetAction{
			Path: cfProcess.Spec.HealthCheck.Data.HTTPEndpoint,
			Port: intstr.FromInt(port),
		}
	case korifiv1alpha1.PortHealthCheckType:
		probeHandler.TCPSocket = &corev1.TCPSocketAction{
			Port: intstr.FromInt(port),
		}
	}

	return probeHandler
}

func startupProbe(cfProcess *korifiv1alpha1.CFProcess, port int) *corev1.Probe {
	if cfProcess.Spec.HealthCheck.Type == korifiv1alpha1.ProcessHealthCheckType {
		return nil
	}

	return &corev1.Probe{
		ProbeHandler:   makeProbeHandler(cfProcess, port),
		TimeoutSeconds: int32(cfProcess.Spec.HealthCheck.Data.InvocationTimeoutSeconds),
		PeriodSeconds:  2,
		FailureThreshold: int32(cfProcess.Spec.HealthCheck.Data.TimeoutSeconds/2 +
			(cfProcess.Spec.HealthCheck.Data.TimeoutSeconds)%2),
	}
}

func livenessProbe(cfProcess *korifiv1alpha1.CFProcess, port int) *corev1.Probe {
	if cfProcess.Spec.HealthCheck.Type == korifiv1alpha1.ProcessHealthCheckType {
		return nil
	}

	return &corev1.Probe{
		ProbeHandler:     makeProbeHandler(cfProcess, port),
		TimeoutSeconds:   int32(cfProcess.Spec.HealthCheck.Data.InvocationTimeoutSeconds),
		PeriodSeconds:    30,
		FailureThreshold: 1,
	}
}

func (r *CFProcessReconciler) SetupWithManager(mgr ctrl.Manager) *builder.Builder {
	return ctrl.NewControllerManagedBy(mgr).
		For(&korifiv1alpha1.CFProcess{}).
		Watches(&source.Kind{Type: &korifiv1alpha1.CFApp{}}, handler.EnqueueRequestsFromMapFunc(func(app client.Object) []reconcile.Request {
			processList := &korifiv1alpha1.CFProcessList{}
			err := mgr.GetClient().List(context.Background(), processList, client.InNamespace(app.GetNamespace()), client.MatchingLabels{korifiv1alpha1.CFAppGUIDLabelKey: app.GetName()})
			if err != nil {
				r.log.Error(err, fmt.Sprintf("Error when trying to list CFProcesses in namespace %q", app.GetNamespace()))
				return []reconcile.Request{}
			}

			var requests []reconcile.Request
			for i := range processList.Items {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&processList.Items[i])})
			}

			return requests
		}))
}

func mebibyteQuantity(miB int64) resource.Quantity {
	return *resource.NewQuantity(miB*1024*1024, resource.BinarySI)
}
