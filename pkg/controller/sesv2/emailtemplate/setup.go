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

package emailtemplate

import (
	"context"
	"time"

	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	svcsdk "github.com/aws/aws-sdk-go/service/sesv2"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	svcapitypes "github.com/crossplane/provider-aws/apis/sesv2/v1alpha1"
	awsclient "github.com/crossplane/provider-aws/pkg/clients"

	"github.com/pkg/errors"
)

const (
	errNotEmailTemplate = "managed resource is not a SES EmailTemplate custom resource"
	errKubeUpdateFailed = "cannot update SES EmailTemplate custom resource"
)

// SetupEmailTemplate adds a controller that reconciles SES EmailTemplate.
func SetupEmailTemplate(mgr ctrl.Manager, l logging.Logger, rl workqueue.RateLimiter, poll time.Duration) error {
	name := managed.ControllerName(svcapitypes.EmailTemplateGroupKind)
	opts := []option{setupExternal}

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&svcapitypes.EmailTemplate{}).
		WithOptions(controller.Options{
			RateLimiter: ratelimiter.NewDefaultManagedRateLimiter(rl),
		}).
		Complete(managed.NewReconciler(mgr,
			resource.ManagedKind(svcapitypes.EmailTemplateGroupVersionKind),
			managed.WithExternalConnecter(&connector{kube: mgr.GetClient(), opts: opts}),
			managed.WithReferenceResolver(managed.NewAPISimpleReferenceResolver(mgr.GetClient())),
			managed.WithInitializers(managed.NewDefaultProviderConfig(mgr.GetClient()), managed.NewNameAsExternalName(mgr.GetClient()), &tagger{kube: mgr.GetClient()}),
			managed.WithPollInterval(poll),
			managed.WithLogger(l.WithValues("controller", name)),
			managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name)))))
}

func setupExternal(e *external) {
	e.isUpToDate = isUpToDate
	e.postObserve = postObserve
}

func isUpToDate(cr *svcapitypes.EmailTemplate, resp *svcsdk.GetEmailTemplateOutput) (bool, error) {
	if cr.Spec.ForProvider.TemplateContent != nil && resp.TemplateContent != nil {
		if awsclient.StringValue(cr.Spec.ForProvider.TemplateContent.HTML) != awsclient.StringValue(resp.TemplateContent.Html) {
			return false, nil
		}
		if awsclient.StringValue(cr.Spec.ForProvider.TemplateContent.Subject) != awsclient.StringValue(resp.TemplateContent.Subject) {
			return false, nil
		}
		if awsclient.StringValue(cr.Spec.ForProvider.TemplateContent.Text) != awsclient.StringValue(resp.TemplateContent.Text) {
			return false, nil
		}
	}
	return true, nil
}

func postObserve(_ context.Context, cr *svcapitypes.EmailTemplate, resp *svcsdk.GetEmailTemplateOutput, obs managed.ExternalObservation, err error) (managed.ExternalObservation, error) {
	if err != nil {
		return managed.ExternalObservation{}, err
	}

	if awsclient.StringValue(resp.TemplateName) == awsclient.StringValue(cr.Spec.ForProvider.TemplateName) {
		cr.Status.SetConditions(xpv1.Available())
	} else {
		cr.Status.SetConditions(xpv1.Creating())
	}

	return obs, nil
}

type tagger struct {
	kube client.Client
}

func (t *tagger) Initialize(ctx context.Context, mg resource.Managed) error {
	cr, ok := mg.(*svcapitypes.EmailTemplate)
	if !ok {
		return errors.New(errNotEmailTemplate)
	}
	return errors.Wrap(t.kube.Update(ctx, cr), errKubeUpdateFailed)
}
