/*
Copyright 2021 The Crossplane Authors.

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

package configurationset

import (
	"context"
	"time"

	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/aws/aws-sdk-go/aws"
	svcsdk "github.com/aws/aws-sdk-go/service/sesv2"
	"github.com/aws/aws-sdk-go/service/sesv2/sesv2iface"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	svcapitypes "github.com/crossplane/provider-aws/apis/sesv2/v1alpha1"
	awsclient "github.com/crossplane/provider-aws/pkg/clients"
	svcutils "github.com/crossplane/provider-aws/pkg/controller/sesv2"

	"github.com/pkg/errors"
)

const (
	errNotConfigurationSet = "managed resource is not a SES ConfigurationSet custom resource"
	errKubeUpdateFailed    = "cannot update SES ConfigurationSet custom resource"
)

// SetupConfigurationSet adds a controller that reconciles SES ConfigurationSet.
func SetupConfigurationSet(mgr ctrl.Manager, l logging.Logger, rl workqueue.RateLimiter, poll time.Duration) error {
	name := managed.ControllerName(svcapitypes.ConfigurationSetGroupKind)
	opts := []option{setupExternal}

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&svcapitypes.ConfigurationSet{}).
		WithOptions(controller.Options{
			RateLimiter: ratelimiter.NewDefaultManagedRateLimiter(rl),
		}).
		Complete(managed.NewReconciler(mgr,
			resource.ManagedKind(svcapitypes.ConfigurationSetGroupVersionKind),
			managed.WithExternalConnecter(&connector{kube: mgr.GetClient(), opts: opts}),
			managed.WithReferenceResolver(managed.NewAPISimpleReferenceResolver(mgr.GetClient())),
			managed.WithInitializers(managed.NewDefaultProviderConfig(mgr.GetClient()), managed.NewNameAsExternalName(mgr.GetClient()), &tagger{kube: mgr.GetClient()}),
			managed.WithPollInterval(poll),
			managed.WithLogger(l.WithValues("controller", name)),
			managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name)))))
}

func setupExternal(e *external) {
	h := &hooks{client: e.client, kube: e.kube}
	e.isUpToDate = isUpToDate
	e.postObserve = postObserve
	e.update = h.update
}

type hooks struct {
	client sesv2iface.SESV2API
	kube   client.Client
}

func isUpToDate(cr *svcapitypes.ConfigurationSet, resp *svcsdk.GetConfigurationSetOutput) (bool, error) {
	if !isUpToDateDeliveryOptions(cr, resp) {
		return false, nil
	}

	if !isUpToDateReputationOptions(cr, resp) {
		return false, nil
	}

	if !isUpToDateSendingOptions(cr, resp) {
		return false, nil
	}

	if !isUpToDateSuppressionOptions(cr, resp) {
		return false, nil
	}

	if !isUpToDateTrackingOptions(cr, resp) {
		return false, nil
	}

	return svcutils.AreTagsUpToDate(cr.Spec.ForProvider.Tags, resp.Tags)
}

// isUpToDateDeliveryOptions checks if DeliveryOptions Object are up to date
func isUpToDateDeliveryOptions(cr *svcapitypes.ConfigurationSet, resp *svcsdk.GetConfigurationSetOutput) bool {
	if cr.Spec.ForProvider.DeliveryOptions != nil && resp.DeliveryOptions != nil {
		if awsclient.StringValue(cr.Spec.ForProvider.DeliveryOptions.SendingPoolName) != awsclient.StringValue(resp.DeliveryOptions.SendingPoolName) {
			return false
		}
		if awsclient.StringValue(cr.Spec.ForProvider.DeliveryOptions.TLSPolicy) != awsclient.StringValue(resp.DeliveryOptions.TlsPolicy) {
			return false
		}
	}
	return true
}

// isUpToDateReputationOptions checks if ReputationOptions Object are up to date
func isUpToDateReputationOptions(cr *svcapitypes.ConfigurationSet, resp *svcsdk.GetConfigurationSetOutput) bool {
	if cr.Spec.ForProvider.ReputationOptions != nil && resp.ReputationOptions != nil {
		if awsclient.BoolValue(cr.Spec.ForProvider.ReputationOptions.ReputationMetricsEnabled) != awsclient.BoolValue(resp.ReputationOptions.ReputationMetricsEnabled) {
			return false
		}
	}
	return true
}

// isUpToDateTrackingOptions checks if TrackingOptions Object are up to date
func isUpToDateTrackingOptions(cr *svcapitypes.ConfigurationSet, resp *svcsdk.GetConfigurationSetOutput) bool {
	if cr.Spec.ForProvider.TrackingOptions != nil && resp.TrackingOptions != nil {
		if awsclient.StringValue(cr.Spec.ForProvider.TrackingOptions.CustomRedirectDomain) != awsclient.StringValue(resp.TrackingOptions.CustomRedirectDomain) {
			return false
		}
	}
	return true
}

// isUpToDateSuppressionOptions checks if SuppressionOptions Object are up to date
func isUpToDateSuppressionOptions(cr *svcapitypes.ConfigurationSet, resp *svcsdk.GetConfigurationSetOutput) bool {
	crSuppressedReasons := make([]*string, 0)
	awsSuppressedReasons := make([]*string, 0)

	if cr.Spec.ForProvider.SuppressionOptions != nil && cr.Spec.ForProvider.SuppressionOptions.SuppressedReasons != nil {
		crSuppressedReasons = append(crSuppressedReasons, cr.Spec.ForProvider.SuppressionOptions.SuppressedReasons...)
	}

	if resp.SuppressionOptions != nil && resp.SuppressionOptions.SuppressedReasons != nil {
		awsSuppressedReasons = resp.SuppressionOptions.SuppressedReasons
	}

	if len(crSuppressedReasons) != len(awsSuppressedReasons) {
		return false
	}

	sortCmp := cmpopts.SortSlices(func(i, j *string) bool {
		return aws.StringValue(i) < aws.StringValue(j)
	})

	return cmp.Equal(crSuppressedReasons, awsSuppressedReasons, sortCmp)

}

// isUpToDateSendingOptions checks if SendingOptions Object are up to date
func isUpToDateSendingOptions(cr *svcapitypes.ConfigurationSet, resp *svcsdk.GetConfigurationSetOutput) bool {
	if cr.Spec.ForProvider.SendingOptions != nil && resp.SendingOptions != nil {
		if awsclient.BoolValue(cr.Spec.ForProvider.SendingOptions.SendingEnabled) != awsclient.BoolValue(resp.SendingOptions.SendingEnabled) {
			return false
		}
	}
	return true
}

func postObserve(_ context.Context, cr *svcapitypes.ConfigurationSet, resp *svcsdk.GetConfigurationSetOutput, obs managed.ExternalObservation, err error) (managed.ExternalObservation, error) {
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	switch awsclient.BoolValue(resp.SendingOptions.SendingEnabled) {
	case true:
		cr.Status.SetConditions(xpv1.Available())
	case false:
		cr.Status.SetConditions(xpv1.Unavailable())
	default:
		cr.Status.SetConditions(xpv1.Creating())
	}

	return obs, nil
}

func (e *hooks) update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) { // nolint:gocyclo
	cr, ok := mg.(*svcapitypes.ConfigurationSet)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errUnexpectedObject)
	}
	// Update Resource is not provided other than individual PUT operation
	// NOTE: Update operation NOT generated by ACK code-generator

	// update DeliveryOptions (PutConfigurationSetDeliveryOptions)
	if cr.Spec.ForProvider.DeliveryOptions != nil {
		deliveryOptionsInput := &svcsdk.PutConfigurationSetDeliveryOptionsInput{
			ConfigurationSetName: cr.Spec.ForProvider.ConfigurationSetName,
			SendingPoolName:      cr.Spec.ForProvider.DeliveryOptions.SendingPoolName,
			TlsPolicy:            cr.Spec.ForProvider.DeliveryOptions.TLSPolicy,
		}
		if _, err := e.client.PutConfigurationSetDeliveryOptionsWithContext(ctx, deliveryOptionsInput); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "update failed")
		}
	}

	// update ReputationOptions (PutConfigurationSetReputationOptions)
	if cr.Spec.ForProvider.ReputationOptions != nil {
		reputationOptionsInput := &svcsdk.PutConfigurationSetReputationOptionsInput{
			ConfigurationSetName:     cr.Spec.ForProvider.ConfigurationSetName,
			ReputationMetricsEnabled: cr.Spec.ForProvider.ReputationOptions.ReputationMetricsEnabled,
		}
		if _, err := e.client.PutConfigurationSetReputationOptionsWithContext(ctx, reputationOptionsInput); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "update failed")
		}
	}

	// update SuppressionOptions (PutConfigurationSetSuppressionOptions)
	var suppresssedReasons []*string
	if cr.Spec.ForProvider.SuppressionOptions != nil {
		suppresssedReasons = cr.Spec.ForProvider.SuppressionOptions.SuppressedReasons
		supressOptionsInput := &svcsdk.PutConfigurationSetSuppressionOptionsInput{
			ConfigurationSetName: cr.Spec.ForProvider.ConfigurationSetName,
			SuppressedReasons:    suppresssedReasons,
		}
		if _, err := e.client.PutConfigurationSetSuppressionOptionsWithContext(ctx, supressOptionsInput); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "update failed")
		}
	}

	// update TrackingOptions (PutConfigurationSetTrackingOptions)
	if cr.Spec.ForProvider.TrackingOptions != nil {
		trackingOptionInput := &svcsdk.PutConfigurationSetTrackingOptionsInput{
			ConfigurationSetName: cr.Spec.ForProvider.ConfigurationSetName,
			CustomRedirectDomain: cr.Spec.ForProvider.TrackingOptions.CustomRedirectDomain,
		}
		if _, err := e.client.PutConfigurationSetTrackingOptionsWithContext(ctx, trackingOptionInput); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "update failed")
		}
	}

	// update SendingOptions (PutConfigurationSetSendingOptions)
	if cr.Spec.ForProvider.SendingOptions != nil {
		sendingOptionInput := &svcsdk.PutConfigurationSetSendingOptionsInput{
			ConfigurationSetName: cr.Spec.ForProvider.ConfigurationSetName,
			SendingEnabled:       cr.Spec.ForProvider.SendingOptions.SendingEnabled,
		}
		if _, err := e.client.PutConfigurationSetSendingOptionsWithContext(ctx, sendingOptionInput); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "update failed")
		}
	}

	// TODO(kelvinwijaya): Updating Tags will be useful if we can determine the resourceArn
	/*
		cr, ok := mg.(*svcapitypes.ConfigurationSet)
		if !ok {
			return managed.ExternalUpdate{}, errors.New(errUnexpectedObject)
		}
		input := GenerateGetConfigurationSetInput(cr)
		resp, err := e.client.GetConfigurationSetWithContext(ctx, input)
		if err != nil {
			return managed.ExternalUpdate{}, awsclient.Wrap(resource.Ignore(IsNotFound, err), errDescribe)
		}
		svcutils.UpdateTagsForResource(e.client, cr.Spec.ForProvider.Tags, resp.ConfigurationSetName)
	*/
	return managed.ExternalUpdate{}, nil
}

type tagger struct {
	kube client.Client
}

func (t *tagger) Initialize(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*svcapitypes.ConfigurationSet)
	if !ok {
		return errors.New(errNotConfigurationSet)
	}
	cr.Spec.ForProvider.Tags = svcutils.AddExternalTags(mg, cr.Spec.ForProvider.Tags)
	return errors.Wrap(t.kube.Update(ctx, cr), errKubeUpdateFailed)
}
