/*
Copyright 2022.

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

package controllers

import (
	"context"

	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	routev1 "github.com/openshift/api/route/v1"

	common "github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/endpoint"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	"github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"

	keystonev1 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta1"
	novav1 "github.com/openstack-k8s-operators/nova-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/nova-operator/pkg/novaapi"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
)

// NovaAPIReconciler reconciles a NovaAPI object
type NovaAPIReconciler struct {
	ReconcilerBase
}

//+kubebuilder:rbac:groups=nova.openstack.org,resources=novaapis,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=nova.openstack.org,resources=novaapis/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=nova.openstack.org,resources=novaapis/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=keystone.openstack.org,resources=keystoneendpoints,verbs=get;list;watch;create;update;patch;delete;

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the NovaAPI object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *NovaAPIReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	l := log.FromContext(ctx)

	// Fetch the NovaAPI instance that needs to be reconciled
	instance := &novav1.NovaAPI{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected.
			// For additional cleanup logic use finalizers. Return and don't requeue.
			l.Info("NovaAPI instance not found, probably deleted before reconciled. Nothing to do.")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		l.Error(err, "Failed to read the NovaAPI instance.")
		return ctrl.Result{}, err
	}

	h, err := helper.NewHelper(
		instance,
		r.Client,
		r.Kclient,
		r.Scheme,
		r.Log,
	)
	if err != nil {
		l.Error(err, "Failed to create lib-common Helper")
		return ctrl.Result{}, err
	}
	util.LogForObject(h, "Reconciling", instance)

	// initialize status fields
	if err = r.initStatus(ctx, h, instance); err != nil {
		return ctrl.Result{}, err
	}

	// Always update the instance status when exiting this function so we can
	// persist any changes happend during the current reconciliation.
	defer func() {
		// update the overall status condition if service is ready
		if allSubConditionIsTrue(instance.Status) {
			instance.Status.Conditions.MarkTrue(
				condition.ReadyCondition, condition.ReadyMessage)
		}
		err := r.patchInstance(ctx, h, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	if !instance.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.reconcileDelete(ctx, h, instance)
	}
	// We create a KeystoneEndpoint CR later and that will automatically get the
	// Nova finalizer. So we need a finalizer on the ourselves too so that
	// during NovaAPI CR delete we can have a chance to remove the finalizer from
	// the our KeystoneEndpoint so that is also deleted.
	updated := controllerutil.AddFinalizer(instance, h.GetFinalizer())
	if updated {
		util.LogForObject(h, "Added finalizer to ourselves", instance)
		// we intentionally return immediately to force the deferred function
		// to persist the Instance with the finalizer. We need to have our own
		// finalizer persisted before we try to create the KeystoneEndpoint with
		// our finalizer to avoid orphaning the KeystoneEndpoint.
		return ctrl.Result{}, nil
	}

	// TODO(gibi): Can we use a simple map[string][string] for hashes?
	// Collect hashes of all the input we depend on so that we can easily
	// detect if something is changed.
	hashes := make(map[string]env.Setter)

	secretHash, result, err := ensureSecret(
		ctx,
		types.NamespacedName{Namespace: instance.Namespace, Name: instance.Spec.Secret},
		// TODO(gibi): add keystoneAuthURL here is that is also passed via
		// the Secret. Also add DB and MQ user name here too if those are
		// passed via the Secret
		[]string{
			instance.Spec.PasswordSelectors.APIDatabase,
			instance.Spec.PasswordSelectors.Service,
			instance.Spec.PasswordSelectors.CellDatabase,
		},
		h.GetClient(),
		&instance.Status.Conditions,
		r.RequeueTimeout,
	)
	if err != nil {
		return result, err
	}

	hashes[instance.Spec.Secret] = env.SetValue(secretHash)

	// all our input checks out so report InputReady
	instance.Status.Conditions.MarkTrue(condition.InputReadyCondition, condition.InputReadyMessage)

	err = r.ensureConfigMaps(ctx, h, instance, &hashes)
	if err != nil {
		return ctrl.Result{}, err
	}

	// create hash over all the different input resources to identify if any of
	// those changed and a restart/recreate is required.
	inputHash, err := hashOfInputHashes(ctx, hashes)
	if err != nil {
		return ctrl.Result{}, err
	}

	instance.Status.Hash[common.InputHashName] = inputHash

	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	result, err = r.ensureDeployment(ctx, h, instance, inputHash)
	if (err != nil || result != ctrl.Result{}) {
		return result, err
	}

	// Only expose the service is the deployment succeeded
	if !instance.Status.Conditions.IsTrue(condition.DeploymentReadyCondition) {
		util.LogForObject(h, "Waiting for the Deployment to become Ready before exposing the sevice in Keystone", instance)
		return ctrl.Result{}, nil
	}

	result, err = r.ensureServiceExposed(ctx, h, instance)
	if (err != nil || result != ctrl.Result{}) {
		// We can ignore RequeueAfter as we are watching the Service and Route resource
		// but we have to return while waiting for the service to be exposed
		return ctrl.Result{}, err
	}

	result, err = r.ensureKeystoneEndpoint(ctx, h, instance)
	if (err != nil || result != ctrl.Result{}) {
		// We can ignore RequeueAfter as we are watching the KeystoneEndpoint resource
		return ctrl.Result{}, err
	}

	util.LogForObject(h, "Successfully reconciled", instance)
	return ctrl.Result{}, nil
}

func (r *NovaAPIReconciler) initStatus(
	ctx context.Context, h *helper.Helper, instance *novav1.NovaAPI,
) error {
	if err := r.initConditions(ctx, h, instance); err != nil {
		return err
	}

	// NOTE(gibi): initialize the rest of the status fields here
	// so that the reconcile loop later can assume they are not nil.
	if instance.Status.Hash == nil {
		instance.Status.Hash = map[string]string{}
	}
	if instance.Status.APIEndpoints == nil {
		instance.Status.APIEndpoints = map[string]string{}
	}

	return nil
}

func (r *NovaAPIReconciler) initConditions(
	ctx context.Context, h *helper.Helper, instance *novav1.NovaAPI,
) error {
	if instance.Status.Conditions == nil {
		instance.Status.Conditions = condition.Conditions{}
		// initialize all conditions to Unknown
		cl := condition.CreateList(
			// TODO(gibi): Initialize each condition the controller reports
			// here to Unknown. By default only the top level Ready condition is
			// created by Conditions.Init()
			condition.UnknownCondition(
				condition.InputReadyCondition,
				condition.InitReason,
				condition.InputReadyInitMessage,
			),
			condition.UnknownCondition(
				condition.ServiceConfigReadyCondition,
				condition.InitReason,
				condition.ServiceConfigReadyInitMessage,
			),
			condition.UnknownCondition(
				condition.DeploymentReadyCondition,
				condition.InitReason,
				condition.DeploymentReadyInitMessage,
			),
			condition.UnknownCondition(
				condition.ExposeServiceReadyCondition,
				condition.InitReason,
				condition.ExposeServiceReadyInitMessage,
			),
			condition.UnknownCondition(
				condition.KeystoneEndpointReadyCondition,
				condition.InitReason,
				"KeystoneEndpoint not created",
			),
		)

		instance.Status.Conditions.Init(&cl)
	}
	return nil
}

func (r *NovaAPIReconciler) ensureConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *novav1.NovaAPI,
	hashes *map[string]env.Setter,
) error {
	err := r.generateConfigs(ctx, h, instance, hashes)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return err
	}
	return nil
}

func (r *NovaAPIReconciler) generateConfigs(
	ctx context.Context, h *helper.Helper, instance *novav1.NovaAPI, hashes *map[string]env.Setter,
) error {
	secret := &corev1.Secret{}
	namespace := instance.GetNamespace()
	secretName := types.NamespacedName{
		Namespace: namespace,
		Name:      instance.Spec.Secret,
	}
	err := h.GetClient().Get(ctx, secretName, secret)
	if err != nil {
		return err
	}

	apiMessageBusSecret := &corev1.Secret{}
	secretName = types.NamespacedName{
		Namespace: instance.Namespace,
		Name:      instance.Spec.APIMessageBusSecretName,
	}
	err = h.GetClient().Get(ctx, secretName, apiMessageBusSecret)
	if err != nil {
		util.LogForObject(
			h, "Failed reading Secret", instance,
			"APIMessageBusSecretName", instance.Spec.APIMessageBusSecretName)
		return err
	}

	templateParameters := map[string]interface{}{
		"service_name":           "nova-api",
		"keystone_internal_url":  instance.Spec.KeystoneAuthURL,
		"nova_keystone_user":     instance.Spec.ServiceUser,
		"nova_keystone_password": string(secret.Data[instance.Spec.PasswordSelectors.Service]),
		"api_db_name":            instance.Spec.APIDatabaseUser, // fixme
		"api_db_user":            instance.Spec.APIDatabaseUser,
		"api_db_password":        string(secret.Data[instance.Spec.PasswordSelectors.APIDatabase]),
		"api_db_address":         instance.Spec.APIDatabaseHostname,
		"api_db_port":            3306,
		"cell_db_name":           instance.Spec.Cell0DatabaseUser, // fixme
		"cell_db_user":           instance.Spec.Cell0DatabaseUser,
		"cell_db_password":       string(secret.Data[instance.Spec.PasswordSelectors.CellDatabase]),
		"cell_db_address":        instance.Spec.Cell0DatabaseHostname,
		"cell_db_port":           3306,
		"openstack_cacert":       "",          // fixme
		"openstack_region_name":  "regionOne", // fixme
		"default_project_domain": "Default",   // fixme
		"default_user_domain":    "Default",   // fixme
		"transport_url":          string(apiMessageBusSecret.Data["transport_url"]),
		"metadata_secret":        "42", // fixme
		"log_file":               "/var/log/nova/nova-api.log",
	}
	extraData := map[string]string{}
	if instance.Spec.CustomServiceConfig != "" {
		extraData["03-nova-override.conf"] = instance.Spec.CustomServiceConfig
	}
	for key, data := range instance.Spec.DefaultConfigOverwrite {
		extraData[key] = data
	}

	cmLabels := labels.GetLabels(
		instance, labels.GetGroupLabel(NovaAPILabelPrefix), map[string]string{},
	)

	err = r.GenerateConfigs(
		ctx, h, instance, hashes, templateParameters, extraData, cmLabels,
	)
	return err
}

func (r *NovaAPIReconciler) ensureDeployment(
	ctx context.Context,
	h *helper.Helper,
	instance *novav1.NovaAPI,
	inputHash string,
) (ctrl.Result, error) {
	ss := statefulset.NewStatefulSet(novaapi.StatefulSet(instance, inputHash, getServiceLabels()), 1)
	ss.SetTimeout(r.RequeueTimeout)
	ctrlResult, err := ss.CreateOrPatch(ctx, h)
	if err != nil && !k8s_errors.IsNotFound(err) {
		util.LogErrorForObject(h, err, "Deployment failed", instance)
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DeploymentReadyErrorMessage,
			err.Error()))
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{} || k8s_errors.IsNotFound(err)) {
		util.LogForObject(h, "Deployment in progress", instance)
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DeploymentReadyRunningMessage))
		// It is OK to return success as we are watching for StatefulSet changes
		return ctrlResult, nil
	}

	instance.Status.ReadyCount = ss.GetStatefulSet().Status.ReadyReplicas
	if instance.Status.ReadyCount > 0 {
		util.LogForObject(h, "Deployment is ready", instance)
		instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)
	} else {
		util.LogForObject(h, "Deployment is not ready", instance, "Status", ss.GetStatefulSet().Status)
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DeploymentReadyRunningMessage))
		// It is OK to return success as we are watching for StatefulSet changes
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil
}

func (r *NovaAPIReconciler) ensureServiceExposed(
	ctx context.Context,
	h *helper.Helper,
	instance *novav1.NovaAPI,
) (ctrl.Result, error) {
	var ports = map[endpoint.Endpoint]endpoint.Data{
		endpoint.EndpointAdmin:    {Port: novaapi.APIServicePort},
		endpoint.EndpointPublic:   {Port: novaapi.APIServicePort},
		endpoint.EndpointInternal: {Port: novaapi.APIServicePort},
	}

	apiEndpoints, ctrlResult, err := endpoint.ExposeEndpoints(
		ctx,
		h,
		novaapi.ServiceName,
		getServiceLabels(),
		ports,
	)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ExposeServiceReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ExposeServiceReadyErrorMessage,
			err.Error()))
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ExposeServiceReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.ExposeServiceReadyRunningMessage))
		return ctrlResult, err
	}
	instance.Status.Conditions.MarkTrue(condition.ExposeServiceReadyCondition, condition.ExposeServiceReadyMessage)

	for k, v := range apiEndpoints {
		apiEndpoints[k] = v + "/v2.1"
	}

	instance.Status.APIEndpoints = apiEndpoints
	return ctrl.Result{}, nil
}

func (r *NovaAPIReconciler) ensureKeystoneEndpoint(
	ctx context.Context,
	h *helper.Helper,
	instance *novav1.NovaAPI,
) (ctrl.Result, error) {
	endpointSpec := keystonev1.KeystoneEndpointSpec{
		ServiceName: novaapi.ServiceName,
		Endpoints:   instance.Status.APIEndpoints,
	}
	endpoint := keystonev1.NewKeystoneEndpoint(
		novaapi.ServiceName,
		instance.Namespace,
		endpointSpec,
		getServiceLabels(),
		10,
	)
	ctrlResult, err := endpoint.CreateOrPatch(ctx, h)
	if err != nil {
		return ctrlResult, err
	}
	c := endpoint.GetConditions().Mirror(condition.KeystoneEndpointReadyCondition)
	if c != nil {
		instance.Status.Conditions.Set(c)
	}

	if (ctrlResult != ctrl.Result{}) {
		// We can ignore RequeueAfter as we are watching the KeystoneEndpoint resource
		return ctrlResult, nil
	}

	return ctrl.Result{}, nil
}

func (r *NovaAPIReconciler) ensureKeystoneEndpointDeletion(
	ctx context.Context,
	h *helper.Helper,
	instance *novav1.NovaAPI,
) error {
	// Remove the finalizer from our KeystoneEndpoint CR
	// This is oddly added automatically when we created KeystoneEndpoint but
	// we need to remove it manually
	endpoint, err := keystonev1.GetKeystoneEndpointWithName(ctx, h, novaapi.ServiceName, instance.Namespace)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return err
	}

	if k8s_errors.IsNotFound(err) {
		// Nothing to do as it was never created
		return nil
	}

	updated := controllerutil.RemoveFinalizer(endpoint, h.GetFinalizer())
	if !updated {
		// No finalizer to remove
		return nil
	}

	if err = h.GetClient().Update(ctx, endpoint); err != nil && !k8s_errors.IsNotFound(err) {
		return err
	}
	util.LogForObject(h, "Removed finalizer from nova KeystoneEndpoint", instance)

	return nil
}

func (r *NovaAPIReconciler) reconcileDelete(
	ctx context.Context,
	h *helper.Helper,
	instance *novav1.NovaAPI,
) error {
	util.LogForObject(h, "Reconciling delete", instance)

	err := r.ensureKeystoneEndpointDeletion(ctx, h, instance)
	if err != nil {
		return err
	}

	// Successfully cleaned up everyting. So as the final step let's remove the
	// finalizer from ourselves to allow the deletion of NovaAPI CR itself
	updated := controllerutil.RemoveFinalizer(instance, h.GetFinalizer())
	if updated {
		util.LogForObject(h, "Removed finalizer from ourselves", instance)
	}

	util.LogForObject(h, "Reconciled delete successfully", instance)
	return nil
}

func getServiceLabels() map[string]string {
	return map[string]string{
		common.AppSelector: NovaAPILabelPrefix,
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *NovaAPIReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&novav1.NovaAPI{}).
		Owns(&v1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&routev1.Route{}).
		Owns(&keystonev1.KeystoneEndpoint{}).
		Complete(r)
}
