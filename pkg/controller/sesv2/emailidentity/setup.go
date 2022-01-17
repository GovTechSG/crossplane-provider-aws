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

package emailidentity

import (
	"context"
	"time"

	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	svcsdk "github.com/aws/aws-sdk-go/service/sesv2"
	"github.com/aws/aws-sdk-go/service/sesv2/sesv2iface"

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
	errNotEmailIdentity = "managed resource is not a SES EmailIdentity custom resource"
	errKubeUpdateFailed = "cannot update SES EmailIdentity custom resource"
)

// SetupEmailIdentity adds a controller that reconciles SES EmailIdentity.
func SetupEmailIdentity(mgr ctrl.Manager, l logging.Logger, rl workqueue.RateLimiter, poll time.Duration) error {
	name := managed.ControllerName(svcapitypes.EmailIdentityGroupKind)
	opts := []option{setupExternal}

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&svcapitypes.EmailIdentity{}).
		WithOptions(controller.Options{
			RateLimiter: ratelimiter.NewController(rl),
		}).
		Complete(managed.NewReconciler(mgr,
			resource.ManagedKind(svcapitypes.EmailIdentityGroupVersionKind),
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
	// e.preObserve = preObserve
	e.postObserve = postObserve
	// e.preCreate = preCreate
	e.postCreate = h.postCreate
	// e.preDelete = preDelete
	e.update = h.update
}

type hooks struct {
	client sesv2iface.SESV2API
	kube   client.Client
}

func isUpToDate(cr *svcapitypes.EmailIdentity, resp *svcsdk.GetEmailIdentityOutput) (bool, error) {
	if awsclient.StringValue(cr.Spec.ForProvider.ConfigurationSetName) != awsclient.StringValue(resp.ConfigurationSetName) {
		return false, nil
	}

	if !isUpToDateMailFromAttributes(cr, resp) {
		return false, nil
	}

	// TODO(kelvinwijaya): isUpToDateDkimSigningAttributes(cr, resp) - GetEmailIdentityOutput.DkimSigningAttributes is not supported in aws-sdk-go v1.37.4

	return svcutils.AreTagsUpToDate(cr.Spec.ForProvider.Tags, resp.Tags)
}

// isUpToDateMailFromAttributes checks if MailFromAttributes Object are up to date
func isUpToDateMailFromAttributes(cr *svcapitypes.EmailIdentity, resp *svcsdk.GetEmailIdentityOutput) bool {
	// Check if MailAttributes is empty in CR
	if cr.Spec.ForProvider.MailFromAttributes != nil && resp.MailFromAttributes != nil {
		if awsclient.StringValue(cr.Spec.ForProvider.MailFromAttributes.BehaviorOnMxFailure) != awsclient.StringValue(resp.MailFromAttributes.BehaviorOnMxFailure) {
			return false
		}
		if awsclient.StringValue(cr.Spec.ForProvider.MailFromAttributes.MailFromDomain) != awsclient.StringValue(resp.MailFromAttributes.MailFromDomain) {
			return false
		}
	}
	return true
}

func postObserve(_ context.Context, cr *svcapitypes.EmailIdentity, resp *svcsdk.GetEmailIdentityOutput, obs managed.ExternalObservation, err error) (managed.ExternalObservation, error) {
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	switch awsclient.BoolValue(resp.VerifiedForSendingStatus) {
	case true:
		cr.Status.SetConditions(xpv1.Available())
	case false:
		cr.Status.SetConditions(xpv1.Unavailable())
	default:
		cr.Status.SetConditions(xpv1.Creating())
	}

	return obs, nil
}

func (e *hooks) postCreate(ctx context.Context, cr *svcapitypes.EmailIdentity, resp *svcsdk.CreateEmailIdentityOutput, cre managed.ExternalCreation, err error) (managed.ExternalCreation, error) {
	// update DKIMSigningAttributes (PutEmailIdentityDkimSigningAttributes)
	// Note: Should be triggered by isUpToDateDkimSigningAttributes(cr, resp)
	// TODO(kelvinwijaya): This is temporary solution until aws-sdk-go version upgrade
	dkimSigningAttributeExternal := "EXTERNAL" // Ref: https://docs.aws.amazon.com/ses/latest/DeveloperGuide/send-email-authentication-dkim-bring-your-own.html
	var dkimSigningAttributesInput *svcsdk.PutEmailIdentityDkimSigningAttributesInput
	if dkimSigningAttributes := cr.Spec.ForProvider.DkimSigningAttributes; dkimSigningAttributes != nil {
		dkimSigningAttributesInput = &svcsdk.PutEmailIdentityDkimSigningAttributesInput{
			EmailIdentity: cr.Spec.ForProvider.EmailIdentity,
			SigningAttributes: &svcsdk.DkimSigningAttributes{
				DomainSigningPrivateKey: cr.Spec.ForProvider.DkimSigningAttributes.DomainSigningPrivateKey,
				DomainSigningSelector:   cr.Spec.ForProvider.DkimSigningAttributes.DomainSigningSelector,
			},
			SigningAttributesOrigin: awsclient.String(dkimSigningAttributeExternal),
		}
		if _, err := e.client.PutEmailIdentityDkimSigningAttributesWithContext(ctx, dkimSigningAttributesInput); err != nil {
			return cre, errors.Wrap(err, "post-create failed")
		}
	} // else - Defaulted to EasyDKIM if no DKIMSignatureAttributes with SigningAttributesOrigin: AWS_SES

	return cre, nil
}

func (e *hooks) update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	// NOTE: Update operation NOT generated by ACK code-generator
	cr, ok := mg.(*svcapitypes.EmailIdentity)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errUnexpectedObject)
	}

	// update DKIMAttributes (PutEmailIdentityDKIMSigningAttributes) - Enabled as part of SES for Domain Validation using DKIM Records
	// NOTE: GetEmailIdentityOutput.DkimSigningAttributes is not supported in aws-sdk-go v1.37.4 - update has to be triggered together with changes in other field e.g. MailFromAttributes
	// This is done in postCreate stage instead

	// update ConfigurationSetAttributes (PutEmailIdentityConfigurationSetAttributes)
	configurationSetAttributesInput := &svcsdk.PutEmailIdentityConfigurationSetAttributesInput{
		ConfigurationSetName: cr.Spec.ForProvider.ConfigurationSetName,
		EmailIdentity:        cr.Spec.ForProvider.EmailIdentity,
	}
	if _, err := e.client.PutEmailIdentityConfigurationSetAttributesWithContext(ctx, configurationSetAttributesInput); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "update failed")
	}

	// update MailFromAttributes (PutEmailIdentityMailFromAttributes)
	// NOTE: Currently MailFromAttributes is not supported in aws-sdk-go v1.37.4 and is declared in Custom-Type
	if mailFromAttributes := cr.Spec.ForProvider.MailFromAttributes; mailFromAttributes != nil {
		mailFromAttributesInput := &svcsdk.PutEmailIdentityMailFromAttributesInput{
			BehaviorOnMxFailure: cr.Spec.ForProvider.MailFromAttributes.BehaviorOnMxFailure,
			EmailIdentity:       cr.Spec.ForProvider.EmailIdentity,
			MailFromDomain:      cr.Spec.ForProvider.MailFromAttributes.MailFromDomain,
		}

		if _, err := e.client.PutEmailIdentityMailFromAttributesWithContext(ctx, mailFromAttributesInput); err != nil {
			return managed.ExternalUpdate{}, errors.Wrap(err, "update failed")
		}
	}

	// All notifications customization should be added through Configuration sets setup
	return managed.ExternalUpdate{}, nil
}

type tagger struct {
	kube client.Client
}

func (t *tagger) Initialize(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*svcapitypes.EmailIdentity)
	if !ok {
		return errors.New(errNotEmailIdentity)
	}
	cr.Spec.ForProvider.Tags = svcutils.AddExternalTags(mg, cr.Spec.ForProvider.Tags)
	return errors.Wrap(t.kube.Update(ctx, cr), errKubeUpdateFailed)
}
