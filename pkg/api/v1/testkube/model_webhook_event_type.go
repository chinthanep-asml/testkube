/*
 * Testkube API
 *
 * Testkube provides a Kubernetes-native framework for test definition, execution and results
 *
 * API version: 1.0.0
 * Contact: testkube@kubeshop.io
 * Generated by: Swagger Codegen (https://github.com/swagger-api/swagger-codegen.git)
 */
package testkube

type WebhookEventType string

// List of WebhookEventType
const (
	START_TEST_WebhookEventType WebhookEventType = "start-test"
	END_TEST_WebhookEventType   WebhookEventType = "end-test"
)
