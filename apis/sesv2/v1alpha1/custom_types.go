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

package v1alpha1

// SES states.
const (
	// The Configuration sets Sending status to Enabled
	ConfigurationSetsSendingStatusEnabled = "Enabled"
	// The Configuration sets Sending status to Disabled
	ConfigurationSetsSendingStatusDisabled = "Disabled"
	// The EmailIdentity DKIMAttributes status is pending
	DkimAttributesStatusPending = "PENDING"
	// The EmailIdentity DKIMAttributes status is successful
	DkimAttributesStatusSuccess = "SUCCESS"
	// The EmailIdentity DKIMAttributes status is failed
	DkimAttributesStatusFailed = "FAILED"
	// The EmailIdentity DKIMAttributes is temporary failed
	DkimAttributesStatusTemporaryFailure = "TEMPORARY_FAILURE"
	// The EmailIdentity DKIMAttributes is not started
	DkimAttributesStatusNotStarted = "NOT_STARTED"
)

// CustomConfigurationSetEventDestinationParameters are parameters for
type CustomConfigurationSetEventDestinationParameters struct{}

// CustomConfigurationSetParameters are parameters for
type CustomConfigurationSetParameters struct{}

// CustomEmailIdentityParameters are parameters for
type CustomEmailIdentityParameters struct {
	// An object that contains information about the Mail-From attributes for the email identity.
	// +optional
	MailFromAttributes *MailFromAttributes `json:"mailFromAttributes,omitempty"`
}

// CustomEmailTemplateParameters are parameters for
type CustomEmailTemplateParameters struct{}
