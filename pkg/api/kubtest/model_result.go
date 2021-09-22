/*
 * Kubtest API
 *
 * Kubtest provides a Kubernetes-native framework for test definition, execution and results
 *
 * API version: 1.0.0
 * Contact: kubtest@kubshop.io
 * Generated by: Swagger Codegen (https://github.com/swagger-api/swagger-codegen.git)
 */
package kubtest

import (
	"time"
)

// execution result returned from executor
type Result struct {
	// execution id
	Id string `json:"id,omitempty"`
	// script metadata content
	ScriptContent string      `json:"scriptContent,omitempty"`
	Repository    *Repository `json:"repository,omitempty"`
	// execution params passed to executor
	Params map[string]string `json:"params,omitempty"`
	// execution status
	Status string           `json:"status,omitempty"`
	Result *ExecutionResult `json:"result,omitempty"`
	// test start time
	StartTime time.Time `json:"startTime,omitempty"`
	// test end time
	EndTime time.Time `json:"endTime,omitempty"`
	// script execution status
	// RAW Script execution output, depends of reporter used in particular tool
	Output string `json:"output,omitempty"`
	// output type depends of reporter used in partucular tool
	OutputType string `json:"outputType,omitempty"`
	// error message when status is error, separate to output as output can be partial in case of error
	ErrorMessage string `json:"errorMessage,omitempty"`
	// execution steps (for collection of requests)
	Steps []ExecutionStepResult `json:"steps,omitempty"`
}
