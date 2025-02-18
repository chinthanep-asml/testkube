package v1

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/valyala/fasthttp"
	"go.mongodb.org/mongo-driver/mongo"

	testsv3 "github.com/kubeshop/testkube-operator/apis/tests/v3"
	"github.com/kubeshop/testkube/internal/pkg/api/repository/result"
	"github.com/kubeshop/testkube/pkg/api/v1/testkube"
	"github.com/kubeshop/testkube/pkg/executor/client"
	"github.com/kubeshop/testkube/pkg/executor/output"
	"github.com/kubeshop/testkube/pkg/types"
	"github.com/kubeshop/testkube/pkg/workerpool"
	"github.com/kubeshop/testkube/pkg/log"
)

const (
	// DefaultConcurrencyLevel is a default concurrency level for worker pool
	DefaultConcurrencyLevel = "10"
	// latestExecutionNo defines the number of relevant latest executions
	latestExecutions = 5

	containerType = "container"
)

// ExecuteTestsHandler calls particular executor based on execution request content and type
func (s TestkubeAPI) ExecuteTestsHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := c.Context()

		log.DefaultLogger.Infow("MULTITENANCY ExecuteTestsHandler() ")
		var request testkube.ExecutionRequest
		err := c.BodyParser(&request)
		if err != nil {
			return s.Error(c, http.StatusBadRequest, fmt.Errorf("test request body invalid: %w", err))
		}

		log.DefaultLogger.Infow("MULTITENANCY ExecuteTestsHandler() ", "request", request)

		if request.Args != nil {
			request.Args, err = testkube.PrepareExecutorArgs(request.Args)
			if err != nil {
				return s.Error(c, http.StatusBadRequest, err)
			}
		}

		id := c.Params("id")

		var tests []testsv3.Test
		if id != "" {
			test, err := s.TestsClient.Get(id)
			if err != nil {
				return s.Error(c, http.StatusInternalServerError, fmt.Errorf("can't get test: %w", err))
			}

			tests = append(tests, *test)
		} else {
			testList, err := s.TestsClient.List(c.Query("selector"))
			if err != nil {
				return s.Error(c, http.StatusInternalServerError, fmt.Errorf("can't get tests: %w", err))
			}

			tests = append(tests, testList.Items...)
		}

		var results []testkube.Execution
		if len(tests) != 0 {
			concurrencyLevel, err := strconv.Atoi(c.Query("concurrency", DefaultConcurrencyLevel))
			if err != nil {
				return s.Error(c, http.StatusBadRequest, fmt.Errorf("can't detect concurrency level: %w", err))
			}

			workerpoolService := workerpool.New[testkube.Test, testkube.ExecutionRequest, testkube.Execution](concurrencyLevel)

			go workerpoolService.SendRequests(s.scheduler.PrepareTestRequests(tests, request))
			go workerpoolService.Run(ctx)

			for r := range workerpoolService.GetResponses() {
				results = append(results, r.Result)
			}
		}

		if id != "" && len(results) != 0 {
			if results[0].ExecutionResult.IsFailed() {
				return s.Error(c, http.StatusInternalServerError, fmt.Errorf(results[0].ExecutionResult.ErrorMessage))
			}

			c.Status(http.StatusCreated)
			return c.JSON(results[0])
		}

		c.Status(http.StatusCreated)
		return c.JSON(results)
	}
}

// ListExecutionsHandler returns array of available test executions
func (s TestkubeAPI) ListExecutionsHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// TODO refactor into some Services (based on some abstraction for CRDs at least / CRUD)
		// should we split this to separate endpoint? currently this one handles
		// endpoints from /executions and from /tests/{id}/executions
		// or should id be a query string as it's some kind of filter?

		filter := getFilterFromRequest(c)

		executions, err := s.ExecutionResults.GetExecutions(c.Context(), filter)
		if err != nil {
			return s.Error(c, http.StatusInternalServerError, err)
		}

		executionTotals, err := s.ExecutionResults.GetExecutionTotals(c.Context(), false, filter)
		if err != nil {
			return s.Error(c, http.StatusInternalServerError, err)
		}

		filteredTotals, err := s.ExecutionResults.GetExecutionTotals(c.Context(), true, filter)
		if err != nil {
			return s.Error(c, http.StatusInternalServerError, err)
		}
		results := testkube.ExecutionsResult{
			Totals:   &executionTotals,
			Filtered: &filteredTotals,
			Results:  mapExecutionsToExecutionSummary(executions),
		}

		return c.JSON(results)
	}
}

func (s TestkubeAPI) ExecutionLogsStreamHandler() fiber.Handler {
	return websocket.New(func(c *websocket.Conn) {
		executionID := c.Params("executionID")
		l := s.Log.With("executionID", executionID)

		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::ExecutionLogsStreamHandler() ", "executionID", executionID)

		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::ExecutionLogsStreamHandler() ", "executionID", executionID)

		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::ExecutionLogsStreamHandler() getting pod logs and passing to websocket", "id", c.Params("id"), "locals", c.Locals, "remoteAddr", c.RemoteAddr(), "localAddr", c.LocalAddr())

		l.Debugw("getting pod logs and passing to websocket", "id", c.Params("id"), "locals", c.Locals, "remoteAddr", c.RemoteAddr(), "localAddr", c.LocalAddr())

		defer func() {
			c.Conn.Close()
		}()

		execution, err := s.ExecutionResults.Get(context.Background(), executionID)
		if err != nil {
			l.Errorw("can't find execuction ", "error", err)
			return
		}

		executor, err := s.getExecutorByTestType(execution.TestType)
		if err != nil {
			l.Errorw("can't get executor", "error", err)
			return
		}

		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::ExecutionLogsStreamHandler() before calling Logs()")
		logs, err := executor.Logs(executionID)
		if err != nil {
			l.Errorw("can't get pod logs", "error", err)
			return
		}
		for logLine := range logs {
			l.Debugw("sending log line to websocket", "line", logLine)
			c.WriteJSON(logLine)
		}
	})
}

// ExecutionLogsHandler streams the logs from a test execution
func (s *TestkubeAPI) ExecutionLogsHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		executionID := c.Params("executionID")

		s.Log.Debug("getting logs", "executionID", executionID)

		ctx := c.Context()

		ctx.SetContentType("text/event-stream")
		ctx.Response.Header.Set("Cache-Control", "no-cache")
		ctx.Response.Header.Set("Connection", "keep-alive")
		ctx.Response.Header.Set("Transfer-Encoding", "chunked")

		ctx.SetBodyStreamWriter(fasthttp.StreamWriter(func(w *bufio.Writer) {
			s.Log.Debug("start streaming logs")
			w.Flush()

			execution, err := s.ExecutionResults.Get(ctx, executionID)
			if err != nil {
				output.PrintError(os.Stdout, fmt.Errorf("could not get execution result for ID %s: %w", executionID, err))
				s.Log.Errorw("getting execution error", "error", err)
				w.Flush()
				return
			}

			if execution.ExecutionResult.IsCompleted() {
				err := s.streamLogsFromResult(execution.ExecutionResult, w)
				if err != nil {
					output.PrintError(os.Stdout, fmt.Errorf("could not get execution result for ID %s: %w", executionID, err))
					s.Log.Errorw("getting execution error", "error", err)
					w.Flush()
				}
				return
			}

			s.streamLogsFromJob(executionID, execution.TestType, w)
		}))

		return nil
	}
}

// GetExecutionHandler returns test execution object for given test and execution id/name
func (s TestkubeAPI) GetExecutionHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := c.Context()
		id := c.Params("id", "")
		executionID := c.Params("executionID")

		var execution testkube.Execution
		var err error

		if id == "" {
			execution, err = s.ExecutionResults.Get(ctx, executionID)
			if err == mongo.ErrNoDocuments {
				execution, err = s.ExecutionResults.GetByName(ctx, executionID)
				if err == mongo.ErrNoDocuments {
					return s.Error(c, http.StatusNotFound, fmt.Errorf("test with execution id/name %s not found", executionID))
				}
			}
			if err != nil {
				return s.Error(c, http.StatusInternalServerError, err)
			}
		} else {
			execution, err = s.ExecutionResults.GetByNameAndTest(ctx, executionID, id)
			if err == mongo.ErrNoDocuments {
				return s.Error(c, http.StatusNotFound, fmt.Errorf("test %s/%s not found", id, executionID))
			}
			if err != nil {
				return s.Error(c, http.StatusInternalServerError, err)
			}
		}

		execution.Duration = types.FormatDuration(execution.Duration)

		testSecretMap := make(map[string]string)
		if execution.TestSecretUUID != "" {
			testSecretMap, err = s.TestsClient.GetSecretTestVars(execution.TestName, execution.TestSecretUUID)
			if err != nil {
				return s.Error(c, http.StatusInternalServerError, err)
			}
		}

		testSuiteSecretMap := make(map[string]string)
		if execution.TestSuiteSecretUUID != "" {
			testSuiteSecretMap, err = s.TestsSuitesClient.GetSecretTestSuiteVars(execution.TestSuiteName, execution.TestSuiteSecretUUID)
			if err != nil {
				return s.Error(c, http.StatusInternalServerError, err)
			}
		}

		for key, value := range testSuiteSecretMap {
			testSecretMap[key] = value
		}

		for key, value := range testSecretMap {
			if variable, ok := execution.Variables[key]; ok && value != "" {
				variable.Value = string(value)
				variable.SecretRef = nil
				execution.Variables[key] = variable
			}
		}

		s.Log.Debugw("get test execution request - debug", "execution", execution)

		return c.JSON(execution)
	}
}

func (s TestkubeAPI) AbortExecutionHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		ctx := c.Context()
		executionID := c.Params("executionID")

		s.Log.Infow("aborting execution", "executionID", executionID)
		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::AbortExecutionHandler() ", "executionID", executionID)
		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::AbortExecutionHandler() ", "ctx", ctx)
		execution, err := s.ExecutionResults.Get(ctx, executionID)
		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::AbortExecutionHandler() ", "execution", execution)
		if err == mongo.ErrNoDocuments {
			return s.Error(c, http.StatusNotFound, fmt.Errorf("test with execution id %s not found", executionID))
		}

		if err != nil {
			return s.Error(c, http.StatusInternalServerError, err)
		}

		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::AbortExecutionHandler() before calling Abort")

		result := s.Executor.Abort(&execution)
		s.Metrics.IncAbortTest(execution.TestType, result.IsFailed())

		return err
	}
}

func (s TestkubeAPI) GetArtifactHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {
		executionID := c.Params("executionID")
		fileName := c.Params("filename")

		// TODO fix this someday :) we don't know 15 mins before release why it's working this way
		// remember about CLI client and Dashboard client too!
		unescaped, err := url.QueryUnescape(fileName)
		if err == nil {
			fileName = unescaped
		}

		unescaped, err = url.QueryUnescape(fileName)
		if err == nil {
			fileName = unescaped
		}

		//// quickfix end

		file, err := s.Storage.DownloadFile(executionID, fileName)
		if err != nil {
			return s.Error(c, http.StatusInternalServerError, err)
		}

		// SendStream promises to close file using io.Close() method
		return c.SendStream(file)
	}
}

// GetArtifacts returns list of files in the given bucket
func (s TestkubeAPI) ListArtifactsHandler() fiber.Handler {
	return func(c *fiber.Ctx) error {

		executionID := c.Params("executionID")
		files, err := s.Storage.ListFiles(executionID)
		if err != nil {
			return s.Error(c, http.StatusInternalServerError, err)
		}

		return c.JSON(files)
	}
}

// streamLogsFromResult writes logs from the output of executionResult to the writer
func (s *TestkubeAPI) streamLogsFromResult(executionResult *testkube.ExecutionResult, w *bufio.Writer) error {
	enc := json.NewEncoder(w)
	fmt.Fprintf(w, "data: ")
	s.Log.Debug("using logs from result")
	output := testkube.ExecutorOutput{
		Type_:   output.TypeResult,
		Content: executionResult.Output,
		Result:  executionResult,
	}
	err := enc.Encode(output)
	if err != nil {
		s.Log.Infow("Encode", "error", err)
		return err
	}
	fmt.Fprintf(w, "\n")
	w.Flush()
	return nil
}

// streamLogsFromJob streams logs in chunks to writer from the running execution
func (s *TestkubeAPI) streamLogsFromJob(executionID, testType string, w *bufio.Writer) {
	enc := json.NewEncoder(w)
	s.Log.Infow("getting logs from Kubernetes job")

	executor, err := s.getExecutorByTestType(testType)
	if err != nil {
		output.PrintError(os.Stdout, err)
		s.Log.Errorw("getting logs error", "error", err)
		w.Flush()
		return
	}

	log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::streamLogsFromJob() before calling Logs()")
	logs, err := executor.Logs(executionID)
	s.Log.Debugw("waiting for jobs channel", "channelSize", len(logs))
	if err != nil {
		output.PrintError(os.Stdout, err)
		s.Log.Errorw("getting logs error", "error", err)
		w.Flush()
		return
	}

	s.Log.Infow("looping through logs channel")
	// loop through pods log lines - it's blocking channel
	// and pass single log output as sse data chunk
	for out := range logs {
		s.Log.Debugw("got log line from pod", "out", out)
		fmt.Fprintf(w, "data: ")
		err = enc.Encode(out)
		if err != nil {
			s.Log.Infow("Encode", "error", err)
		}
		// enc.Encode adds \n and we need \n\n after `data: {}` chunk
		fmt.Fprintf(w, "\n")
		w.Flush()
	}
}

func mapExecutionsToExecutionSummary(executions []testkube.Execution) []testkube.ExecutionSummary {
	result := make([]testkube.ExecutionSummary, len(executions))

	for i, execution := range executions {
		result[i] = testkube.ExecutionSummary{
			Id:         execution.Id,
			Name:       execution.Name,
			Number:     execution.Number,
			TestName:   execution.TestName,
			TestType:   execution.TestType,
			Status:     execution.ExecutionResult.Status,
			StartTime:  execution.StartTime,
			EndTime:    execution.EndTime,
			Duration:   types.FormatDuration(execution.Duration),
			DurationMs: types.FormatDurationMs(execution.Duration),
			Labels:     execution.Labels,
		}
	}

	return result
}

// GetLatestExecutionLogs returns the latest executions' logs
func (s *TestkubeAPI) GetLatestExecutionLogs(c context.Context) (map[string][]string, error) {
	log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::GetLatestExecutionLogs() ")
	latestExecutions, err := s.getNewestExecutions(c)
	log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::GetLatestExecutionLogs() after latestExecutions")
	if err != nil {
		return nil, fmt.Errorf("could not list executions: %w", err)
	}

	executionLogs := map[string][]string{}
	for _, e := range latestExecutions {
		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::GetLatestExecutionLogs() loop latestExecutions", "e", e)
		logs, err := s.getExecutionLogs(e)
		log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::GetLatestExecutionLogs() loop latestExecutions", "logs", logs)
		if err != nil {
			return nil, fmt.Errorf("could not get logs: %w", err)
		}
		executionLogs[e.Id] = logs
	}

	return executionLogs, nil
}

// getNewestExecutions returns the latest Testkube executions
func (s *TestkubeAPI) getNewestExecutions(c context.Context) ([]testkube.Execution, error) {
	f := result.NewExecutionsFilter().WithPage(1).WithPageSize(latestExecutions)
	executions, err := s.ExecutionResults.GetExecutions(c, f)
	if err != nil {
		return []testkube.Execution{}, fmt.Errorf("could not get executions from repo: %w", err)
	}
	return executions, nil
}

// getExecutionLogs returns logs from an execution
func (s *TestkubeAPI) getExecutionLogs(execution testkube.Execution) ([]string, error) {
	result := []string{}
	if execution.ExecutionResult.IsCompleted() {
		return append(result, execution.ExecutionResult.Output), nil
	}

	log.DefaultLogger.Infow("MULTITENANCY TestkubeAPI::getNewestExecutions() before calling Logs()")

	logs, err := s.Executor.Logs(execution.Id)
	if err != nil {
		return []string{}, fmt.Errorf("could not get logs for execution %s: %w", execution.Id, err)
	}

	for out := range logs {
		result = append(result, out.Result.Output)
	}

	return result, nil
}

func (s *TestkubeAPI) getExecutorByTestType(testType string) (client.Executor, error) {
	executorCR, err := s.ExecutorsClient.GetByType(testType)
	if err != nil {
		return nil, fmt.Errorf("can't get executor spec: %w", err)
	}
	switch executorCR.Spec.ExecutorType {
	case containerType:
		return s.ContainerExecutor, nil
	default:
		return s.Executor, nil
	}
}
