package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"
	"time"

	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"

	"github.com/kubeshop/testkube/internal/pkg/api"
	"github.com/kubeshop/testkube/internal/pkg/api/repository/result"
	"github.com/kubeshop/testkube/pkg/api/v1/testkube"
	"github.com/kubeshop/testkube/pkg/config"
	"github.com/kubeshop/testkube/pkg/event"
	"github.com/kubeshop/testkube/pkg/executor"
	"github.com/kubeshop/testkube/pkg/executor/output"
	"github.com/kubeshop/testkube/pkg/k8sclient"
	"github.com/kubeshop/testkube/pkg/log"
	"github.com/kubeshop/testkube/pkg/telemetry"
	"github.com/kubeshop/testkube/pkg/utils"
)

const (
	// GitUsernameSecretName is git username secret name
	GitUsernameSecretName = "git-username"
	// GitUsernameEnvVarName is git username environment var name
	GitUsernameEnvVarName = "RUNNER_GITUSERNAME"
	// GitTokenSecretName is git token secret name
	GitTokenSecretName = "git-token"
	// GitTokenEnvVarName is git token environment var name
	GitTokenEnvVarName = "RUNNER_GITTOKEN"

	pollTimeout  = 24 * time.Hour
	pollInterval = 200 * time.Millisecond
	volumeDir    = "/data"
	// pollJobStatus is interval for checking if job timeout occurred
	pollJobStatus = 1 * time.Second
	// timeoutIndicator is string that is added to job logs when timeout occurs
	timeoutIndicator = "DeadlineExceeded"
)

// NewJobExecutor creates new job executor
func NewJobExecutor(repo result.Repository, namespace string, images executor.Images, templates executor.Templates,
	serviceAccountName string, metrics ExecutionCounter, emiter *event.Emitter, configMap config.Repository) (client *JobExecutor, err error) {
	/* CHINTHAN */
	var clientSet *kubernetes.Clientset = nil
	/* CHINTHAN */

	/* OLD */
	/*log.DefaultLogger.Infow("MULTITENANCY Creating k8sclient")
	clientSet, err := k8sclient.ConnectToK8s()
	if err != nil {
		return client, err
	}*/
	/* OLD */

	return &JobExecutor{
		ClientSet:          clientSet,
		Repository:         repo,
		Log:                log.DefaultLogger,
		Namespace:          namespace,
		images:             images,
		templates:          templates,
		serviceAccountName: serviceAccountName,
		metrics:            metrics,
		Emitter:            emiter,
		configMap:          configMap,
	}, nil
}

type ExecutionCounter interface {
	IncExecuteTest(execution testkube.Execution)
}

// JobExecutor is container for managing job executor dependencies
type JobExecutor struct {
	Repository         result.Repository
	Log                *zap.SugaredLogger
	ClientSet          *kubernetes.Clientset
	Namespace          string
	Cmd                string
	images             executor.Images
	templates          executor.Templates
	serviceAccountName string
	metrics            ExecutionCounter
	Emitter            *event.Emitter
	configMap          config.Repository
}

type JobOptions struct {
	Name                  string
	Namespace             string
	Image                 string
	ImagePullSecrets      []string
	ImageOverride         string
	Jsn                   string
	TestName              string
	InitImage             string
	JobTemplate           string
	SecretEnvs            map[string]string
	HTTPProxy             string
	HTTPSProxy            string
	UsernameSecret        *testkube.SecretRef
	TokenSecret           *testkube.SecretRef
	Variables             map[string]testkube.Variable
	ActiveDeadlineSeconds int64
	ServiceAccountName    string
}

// Logs returns job logs stream channel using kubernetes api
func (c JobExecutor) Logs(id string) (out chan output.Output, err error) {
	
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Logs() ", "id", id)
	
	out = make(chan output.Output)
	logs := make(chan []byte)

	go func() {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Logs() func()")
		defer func() {
			c.Log.Debug("closing JobExecutor.Logs out log")
			close(out)
		}()
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Logs() before calling TailJobLogs() ", "id", id)
		if err := c.TailJobLogs(id, logs); err != nil {
			out <- output.NewOutputError(err)
			return
		}

		for l := range logs {
			entry, err := output.GetLogEntry(l)
			if err != nil {
				out <- output.NewOutputError(err)
				return
			}
			out <- entry
		}
	}()

	return
}

// Execute starts new external test execution, reads data and returns ID
// Execution is started asynchronously client can check later for results
func (c JobExecutor) Execute(execution *testkube.Execution, options ExecuteOptions) (result testkube.ExecutionResult, err error) {
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() ")
	/* CHINTHAN */
	clientSet, err := k8sclient.ConnectToK8sExecutor()
	if err != nil {
		return result.Err(err), err
	}
	c.ClientSet = clientSet
	c.Namespace = "klarriofc000tstkube1-testkube"
	c.serviceAccountName = "testkube-service-account"
	/* CHINTHAN */

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() ", "ClientSet", c.ClientSet)
	
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() about to call NewRunningExecutionResult()")
	result = testkube.NewRunningExecutionResult()

	ctx := context.Background()
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() about to call CreateJob()")
	err = c.CreateJob(ctx, *execution, options)
	if err != nil {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() CreateJob failed ", "error", err)
		return result.Err(err), err
	}
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() after creating job")
	go c.MonitorJobForTimeout(ctx, execution.Id)

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() c.ClientSet.CoreV1().Pods")
	podsClient := c.ClientSet.CoreV1().Pods(c.Namespace)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() after c.ClientSet.CoreV1().Pods(c.Namespace")
	pods, err := executor.GetJobPods(podsClient, execution.Id, 1, 10)
	if err != nil {
		return result.Err(err), err
	}

	//log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute()", "pods", pods)

	l := c.Log.With("executionID", execution.Id, "type", "async")
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() execution.Id ", "execution.Id", execution.Id)

	for _, pod := range pods.Items {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() looping pods items ", "pod.Labels", pod.Labels)
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() looping pods items ", "pod.Labels[job-name]", pod.Labels["job-name"])
		if pod.Status.Phase != corev1.PodRunning && pod.Labels["job-name"] == execution.Id {
			log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() looping pods items async wait for complete status or error")
			// async wait for complete status or error
			go func(pod corev1.Pod) {
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() looping pods items func()")
				_, err := c.updateResultsFromPod(ctx, pod, l, execution, result)
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Execute() looping pods items func() after updateResultsFromPod")
				if err != nil {
					l.Errorw("update results from jobs pod error", "error", err)
				}
			}(pod)

			return result, nil
		}
	}

	l.Debugw("no pods was found", "totalPodsCount", len(pods.Items))

	return testkube.NewRunningExecutionResult(), nil
}

// Execute starts new external test execution, reads data and returns ID
// Execution is started synchronously client will be blocked
func (c JobExecutor) ExecuteSync(execution *testkube.Execution, options ExecuteOptions) (result testkube.ExecutionResult, err error) {
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::ExecuteSync() ")
	/* CHINTHAN */
	clientSet, err := k8sclient.ConnectToK8sExecutor()
	if err != nil {
		return result.Err(err), err
	}
	c.ClientSet = clientSet
	c.Namespace = "klarriofc000tstkube1-testkube"
	c.serviceAccountName = "testkube-service-account"
	/* CHINTHAN */

	//c.ClientSet = clientSet

	result = testkube.NewRunningExecutionResult()

	ctx := context.Background()
	err = c.CreateJob(ctx, *execution, options)
	if err != nil {
		return result.Err(err), err
	}

	podsClient := c.ClientSet.CoreV1().Pods(c.Namespace)
	pods, err := executor.GetJobPods(podsClient, execution.Id, 1, 10)
	if err != nil {
		return result.Err(err), err
	}

	l := c.Log.With("executionID", execution.Id, "type", "sync")

	// get job pod and
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning && pod.Labels["job-name"] == execution.Id {
			return c.updateResultsFromPod(ctx, pod, l, execution, result)
		}
	}

	l.Debugw("no pods was found", "totalPodsCount", len(pods.Items))

	return

}

func (c JobExecutor) MonitorJobForTimeout(ctx context.Context, jobName string) {
	
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::MonitorJobForTimeout() ")

	ticker := time.NewTicker(pollJobStatus)
	l := c.Log.With("jobName", jobName)
	for {
		select {
		case <-ctx.Done():
			l.Infow("context done, stopping job timeout monitor")
			return
		case <-ticker.C:
			jobs, err := c.ClientSet.BatchV1().Jobs(c.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + jobName})
			if err != nil {
				l.Errorw("could not get jobs", "error", err)
				return
			}
			if jobs == nil || len(jobs.Items) == 0 {
				return
			}

			job := jobs.Items[0]

			if job.Status.Succeeded > 0 {
				l.Debugw("job succeeded", "status")
				return
			}

			if job.Status.Failed > 0 {
				l.Debugw("job failed")
				if len(job.Status.Conditions) > 0 {
					for _, condition := range job.Status.Conditions {
						l.Infow("job timeout", "condition.reason", condition.Reason)
						if condition.Reason == timeoutIndicator {
							c.Timeout(jobName)
						}
					}
				}
				return
			}

			if job.Status.Active > 0 {
				continue
			}
		}
	}
}

// CreateJob creates new Kubernetes job based on execution and execute options
func (c JobExecutor) CreateJob(ctx context.Context, execution testkube.Execution, options ExecuteOptions) error {
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::CreateJob() ", "c.Namespace", c.Namespace)
	jobs := c.ClientSet.BatchV1().Jobs(c.Namespace)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::CreateJob() Created BatchV1().Jobs", "jobs", jobs)
	jobOptions, err := NewJobOptions(c.images.Init, c.templates.Job, c.serviceAccountName, execution, options)
	if err != nil {
		return err
	}

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::CreateJob() Creating job with options", "jobOptions", jobOptions)

	c.Log.Debug("creating job with options", "options", jobOptions)
	jobSpec, err := NewJobSpec(c.Log, jobOptions)
	if err != nil {
		return err
	}

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::CreateJob() ", "jobSpec", jobSpec)

	_, err = jobs.Create(ctx, jobSpec, metav1.CreateOptions{})
	return err
}

// updateResultsFromPod watches logs and stores results if execution is finished
func (c JobExecutor) updateResultsFromPod(ctx context.Context, pod corev1.Pod, l *zap.SugaredLogger, execution *testkube.Execution, result testkube.ExecutionResult) (testkube.ExecutionResult, error) {
	var err error

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() ")

	// save stop time and final state
	defer c.stopExecution(ctx, l, execution, &result, err)

	// wait for complete
	l.Debug("poll immediate waiting for pod to succeed")
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() poll immediate waiting for pod to succeed")
	if err = wait.PollImmediate(pollInterval, pollTimeout, executor.IsPodReady(c.ClientSet, pod.Name, c.Namespace)); err != nil {
		// continue on poll err and try to get logs later
		l.Errorw("waiting for pod complete error", "error", err)
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() waiting for pod complete error", "error", err)
	}
	l.Debug("poll immediate end")
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() poll immediate end")

	var logs []byte
	logs, err = executor.GetPodLogs(c.ClientSet, c.Namespace, pod)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() after GetPodLogs")
	if err != nil {
		l.Errorw("get pod logs error", "error", err)
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() get pod logs error", "error", err)
		return result, err
	}

	// parse job ouput log (JSON stream)
	result, _, err = output.ParseRunnerOutput(logs)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() after ParseRunnerOutput ", "result", result)
	if err != nil {
		l.Errorw("parse ouput error", "error", err)
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() parse ouput error", "error", err)
		return result, err
	}
	// saving result in the defer function
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::updateResultsFromPod() saving result in the defer function")
	return result, nil

}

func (c JobExecutor) stopExecution(ctx context.Context, l *zap.SugaredLogger, execution *testkube.Execution, result *testkube.ExecutionResult, passedErr error) {
	savedExecution, err := c.Repository.Get(ctx, execution.Id)

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() ")

	l.Debugw("stopping execution", "executionId", execution.Id, "status", result.Status, "executionStatus", execution.ExecutionResult.Status, "passedError", passedErr, "savedExecutionStatus", savedExecution.ExecutionResult.Status)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() stopping execution", "executionId", execution.Id, "status", result.Status, "executionStatus", execution.ExecutionResult.Status, "passedError", passedErr, "savedExecutionStatus", savedExecution.ExecutionResult.Status)
	if err != nil {
		l.Errorw("get execution error", "error", err)
	}

	if savedExecution.IsCanceled() || savedExecution.IsTimeout() {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() canceled or timedout")
		return
	}

	execution.Stop()
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after execution.Stopped")
	err = c.Repository.EndExecution(ctx, *execution)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after EndExecution")
	if err != nil {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() update execution result error")
		l.Errorw("Update execution result error", "error", err)
	}

	if passedErr != nil {
		*result = result.Err(passedErr)
	}

	eventToSend := testkube.NewEventEndTestSuccess(execution)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after eventToSend")
	if result.IsAborted() {
		result.Output = result.Output + "\nTest run was aborted manually."
		eventToSend = testkube.NewEventEndTestAborted(execution)
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() Test run was aborted manually")
	} else if result.IsTimeout() {
		result.Output = result.Output + "\nTest run was aborted due to timeout."
		eventToSend = testkube.NewEventEndTestTimeout(execution)
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() Test run was aborted due to timeout")
	}

	// metrics increase
	execution.ExecutionResult = result
	l.Infow("execution ended, saving result", "executionId", execution.Id, "status", result.Status)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() execution ended, saving result", "executionId", execution.Id, "status", result.Status)

	err = c.Repository.UpdateResult(ctx, execution.Id, *result)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after UpdateResult")

	if err != nil {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after update execution result error")
		l.Errorw("Update execution result error", "error", err)
	}

	c.metrics.IncExecuteTest(*execution)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after IncExecuteTest")
	c.Emitter.Notify(eventToSend)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after Notify")

	telemetryEnabled, err := c.configMap.GetTelemetryEnabled(ctx)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after GetTelemetryEnabled")
	if err != nil {
		l.Debugw("getting telemetry enabled error", "error", err)
	}

	if !telemetryEnabled {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() telemetry not enabled")
		return
	}

	clusterID, err := c.configMap.GetUniqueClusterId(ctx)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after GetUniqueClusterId")
	if err != nil {
		l.Debugw("getting cluster id error", "error", err)
	}

	host, err := os.Hostname()
	if err != nil {
		l.Debugw("getting hostname error", "hostname", host, "error", err)
	}

	var dataSource string
	if execution.Content != nil {
		dataSource = execution.Content.Type_
	}

	status := ""
	if execution.ExecutionResult != nil && execution.ExecutionResult.Status != nil {
		status = string(*execution.ExecutionResult.Status)
	}

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() before calling SendRunEvent")
	out, err := telemetry.SendRunEvent("testkube_api_run_test", telemetry.RunParams{
		AppVersion: api.Version,
		DataSource: dataSource,
		Host:       host,
		ClusterID:  clusterID,
		TestType:   execution.TestType,
		DurationMs: execution.DurationMs,
		Status:     status,
	})
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() after SEndRunEvent")
	if err != nil {
		l.Debugw("sending run test telemetry event error", "error", err)
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() sending run test telemetry event error", "error", err)
	} else {
		l.Debugw("sending run test telemetry event", "output", out)
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::stopExecution() sending run test telemetry event", "output", out)
	}
}

// NewJobOptionsFromExecutionOptions compose JobOptions based on ExecuteOptions
func NewJobOptionsFromExecutionOptions(options ExecuteOptions) JobOptions {
	return JobOptions{
		Image:                 options.ExecutorSpec.Image,
		ImageOverride:         options.ImageOverride,
		JobTemplate:           options.ExecutorSpec.JobTemplate,
		TestName:              options.TestName,
		Namespace:             options.Namespace,
		SecretEnvs:            options.Request.SecretEnvs,
		HTTPProxy:             options.Request.HttpProxy,
		HTTPSProxy:            options.Request.HttpsProxy,
		UsernameSecret:        options.UsernameSecret,
		TokenSecret:           options.TokenSecret,
		ActiveDeadlineSeconds: options.Request.ActiveDeadlineSeconds,
	}
}

// TailJobLogs - locates logs for job pod(s)
func (c *JobExecutor) TailJobLogs(id string, logs chan []byte) (err error) {

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() before changing anything", "id", id, "c.Namespace", c.Namespace)

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() before changing anything full ", "full jobexecutor ", c)

	/* CHINTHAN */
	clientSet, err := k8sclient.ConnectToK8sExecutor()
	if err != nil {
		log.DefaultLogger.Errorw("MULTITENANCY JobExecutor::TailJobLogs() unable to connect to k8s")
		return err
	}
	c.ClientSet = clientSet
	c.Namespace = "klarriofc000tstkube1-testkube"
	c.serviceAccountName = "testkube-service-account"
	/* CHINTHAN */

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() ", "ClientSet", c.ClientSet)

	podsClient := c.ClientSet.CoreV1().Pods(c.Namespace)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() got podsClient")
	ctx := context.Background()
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() got context.Bg()")

	pods, err := executor.GetJobPods(podsClient, id, 1, 10)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() got pods")
	if err != nil {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() error in getting pods")
		close(logs)
		return err
	}

	
	for _, pod := range pods.Items {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() looping ", "pod.Labels", pod.Labels, "id", id)
		if pod.Labels["job-name"] == id {
			log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() looping id found ", "podNamespace", pod.Namespace, "podName", pod.Name, "podStatus", pod.Status)
			l := c.Log.With("podNamespace", pod.Namespace, "podName", pod.Name, "podStatus", pod.Status)

			switch pod.Status.Phase {

			case corev1.PodRunning:
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() looping corev1.PodRunning: tailing pods logs: immediately")
				l.Debug("tailing pod logs: immediately")
				return c.TailPodLogs(ctx, pod, logs)

			case corev1.PodFailed:
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() looping corev1.PodFailed: can't get pods logs, pod failed")
				err := fmt.Errorf("can't get pod logs, pod failed: %s/%s", pod.Namespace, pod.Name)
				l.Errorw(err.Error())
				return c.GetLastLogLineError(ctx, pod)

			default:
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() looping default: tailing job logs: waiting for pod to be ready")
				l.Debugw("tailing job logs: waiting for pod to be ready")
				if err = wait.PollImmediate(pollInterval, pollTimeout, executor.IsPodLoggable(c.ClientSet, pod.Name, c.Namespace)); err != nil {
					log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() looping default: poll immediate error when tailing logs")
					l.Errorw("poll immediate error when tailing logs", "error", err)
					return c.GetLastLogLineError(ctx, pod)
				}

				l.Debug("tailing pod logs")
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailJobLogs() looping default: tailing pod logs")
				return c.TailPodLogs(ctx, pod, logs)
			}
		}
	}

	return
}

func (c *JobExecutor) TailPodLogs(ctx context.Context, pod corev1.Pod, logs chan []byte) (err error) {
	
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() ", "pod", pod)

	count := int64(1)

	var containers []string
	for _, container := range pod.Spec.InitContainers {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() loop InitContainers ", "container.Name", container.Name, "container", container)
		containers = append(containers, container.Name)
	}

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() after InitContainers append ", "containers", containers)

	for _, container := range pod.Spec.Containers {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() loop Containers ", "container.Name", container.Name, "container", container)
		containers = append(containers, container.Name)
	}

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() after Containers append ", "containers", containers)

	go func() {
		defer close(logs)

		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() ")

		for _, container := range containers {
			log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() looping containers ")
			podLogOptions := corev1.PodLogOptions{
				Follow:    true,
				TailLines: &count,
				Container: container,
			}

			log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() looping containers after podlogopions")

			podLogRequest := c.ClientSet.CoreV1().
				Pods(c.Namespace).
				GetLogs(pod.Name, &podLogOptions)

			log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() looping containers after podlogrequest")

			stream, err := podLogRequest.Stream(ctx)
			if err != nil {
				c.Log.Errorw("stream error", "error", err)
				continue
			}

			log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() looping containers after podlogrequest.stream")

			reader := bufio.NewReader(stream)

			log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() looping containers after newreader")

			for {
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() looping containers for")
				b, err := utils.ReadLongLine(reader)
				if err != nil {
					if err == io.EOF {
						err = nil
					}
					break
				}
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() looping containers TailPodLogs stream scan", "out", b, "pod", pod.Name)
				c.Log.Debug("TailPodLogs stream scan", "out", b, "pod", pod.Name)
				logs <- b
			}
			log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() after for loop")

			if err != nil {
				log.DefaultLogger.Infow("MULTITENANCY JobExecutor::TailPodLogs() func() looping containers scanner error", "error", err)
				c.Log.Errorw("scanner error", "error", err)
			}
		}
	}()
	return
}

// GetPodLogError returns last line as error
func (c *JobExecutor) GetPodLogError(ctx context.Context, pod corev1.Pod) (logsBytes []byte, err error) {
	// error line should be last one
	return executor.GetPodLogs(c.ClientSet, c.Namespace, pod, 1)
}

// GetLastLogLineError return error if last line is failed
func (c *JobExecutor) GetLastLogLineError(ctx context.Context, pod corev1.Pod) error {
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::GetLastLogLineError() ")

	l := c.Log.With("pod", pod.Name, "namespace", pod.Namespace)
	log, err := c.GetPodLogError(ctx, pod)
	if err != nil {
		return fmt.Errorf("getPodLogs error: %w", err)
	}

	l.Debugw("log", "got last log bytes", string(log)) // in case distorted log bytes
	entry, err := output.GetLogEntry(log)
	if err != nil {
		return fmt.Errorf("GetLogEntry error: %w", err)
	}

	c.Log.Errorw("got last log entry", "log", entry.String())
	return fmt.Errorf("error from last log entry: %s", entry.String())
}

// AbortK8sJob aborts K8S by job name
func (c *JobExecutor) Abort(execution *testkube.Execution) (result *testkube.ExecutionResult) {
	
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Abort() ", "c.Namespace", c.Namespace, "execution.Id", execution.Id, "c.ClientSet", c.ClientSet)

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Abort() ", "execution", execution)

	clientSet, err := k8sclient.ConnectToK8sExecutor()
	if err != nil {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Abort() ", "error", err)
		return
	}
	c.ClientSet = clientSet
	c.Namespace = "klarriofc000tstkube1-testkube"
	c.serviceAccountName = "testkube-service-account"

	l := c.Log.With("execution", execution.Id)
	ctx := context.Background()
	result = executor.AbortJob(c.ClientSet, c.Namespace, execution.Id)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Abort() job aborted", "execution", execution.Id, "result", result)
	c.Log.Debugw("job aborted", "execution", execution.Id, "result", result)
	c.stopExecution(ctx, l, execution, result, nil)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Abort() stopExecution called")
	return
}

func (c *JobExecutor) Timeout(jobName string) (result *testkube.ExecutionResult) {
	
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Timeout() ")

	l := c.Log.With("jobName", jobName)
	l.Infow("job timeout")
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Timeout job timeout")
	ctx := context.Background()
	execution, err := c.Repository.Get(ctx, jobName)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Timeout ", "execution", execution)
	if err != nil {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::Timeout error getting execution ", "error", err)
		l.Errorw("error getting execution", "error", err)
		return
	}
	result = &testkube.ExecutionResult{
		Status: testkube.ExecutionStatusTimeout,
	}
	c.stopExecution(ctx, l, &execution, result, nil)

	return
}

// NewJobSpec is a method to create new job spec
func NewJobSpec(log *zap.SugaredLogger, options JobOptions) (*batchv1.Job, error) {
	
	log.Infow("MULTITENANCY JobExecutor::NewJobSpec() ")

	secretEnvVars := executor.PrepareSecretEnvs(options.SecretEnvs, options.Variables,
		options.UsernameSecret, options.TokenSecret)

	tmpl, err := template.New("job").Parse(options.JobTemplate)
	if err != nil {
		return nil, fmt.Errorf("creating job spec from options.JobTemplate error: %w", err)
	}

	options.Jsn = strings.ReplaceAll(options.Jsn, "'", "''")
	var buffer bytes.Buffer
	if err = tmpl.ExecuteTemplate(&buffer, "job", options); err != nil {
		return nil, fmt.Errorf("executing job spec template: %w", err)
	}

	var job batchv1.Job
	jobSpec := buffer.String()
	log.Debug("Job specification", jobSpec)
	
	log.Infow("MULTITENANCY JobExecutor::NewJobSpec()", "jobSpec", jobSpec)

	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(jobSpec), len(jobSpec))
	if err := decoder.Decode(&job); err != nil {
		return nil, fmt.Errorf("decoding job spec error: %w", err)
	}

	env := append(executor.RunnerEnvVars, secretEnvVars...)
	if options.HTTPProxy != "" {
		env = append(env, corev1.EnvVar{Name: "HTTP_PROXY", Value: options.HTTPProxy})
	}

	if options.HTTPSProxy != "" {
		env = append(env, corev1.EnvVar{Name: "HTTPS_PROXY", Value: options.HTTPSProxy})
	}

	for i := range job.Spec.Template.Spec.InitContainers {
		job.Spec.Template.Spec.InitContainers[i].Env = append(job.Spec.Template.Spec.InitContainers[i].Env, env...)
	}

	for i := range job.Spec.Template.Spec.Containers {
		job.Spec.Template.Spec.Containers[i].Env = append(job.Spec.Template.Spec.Containers[i].Env, env...)
		// override container image if provided
		if options.ImageOverride != "" {
			job.Spec.Template.Spec.Containers[i].Image = options.ImageOverride
		}
	}

	return &job, nil
}

func NewJobOptions(initImage, jobTemplate string, serviceAccountName string, execution testkube.Execution, options ExecuteOptions) (jobOptions JobOptions, err error) {
	/* CHINTHAN */
	execution.TestNamespace = "klarriofc000tstkube1-testkube"
	/* CHINTHAN */
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions() (beginning)", "execution", execution)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions() (beginning)", "options", options)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions() (beginning)", "initImage", initImage)
	initImage = "europe-docker.pkg.dev/asml-dpng-dev-01/asml-ngdp-test-registry/docker.io/kubeshop/testkube-executor-init:SNAPSHOT"

	jsn, err := json.Marshal(execution)
	if err != nil {
		return jobOptions, err
	}

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions()", "jsn", jsn)

	jobOptions = NewJobOptionsFromExecutionOptions(options)
	jobOptions.Name = execution.Id
	jobOptions.Namespace = execution.TestNamespace
	/* CHINTHAN */
	jobOptions.Namespace = "klarriofc000tstkube1-testkube"
	/* CHINTHAN */
	jobOptions.Jsn = string(jsn)
	jobOptions.InitImage = initImage
	jobOptions.TestName = execution.TestName
	if jobOptions.JobTemplate == "" {
		log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions() jobtemplate empty. setting it new ", "jobTemplate", jobTemplate)
		jobOptions.JobTemplate = jobTemplate
	}
	jobOptions.Variables = execution.Variables
	jobOptions.ImagePullSecrets = options.ImagePullSecretNames
	jobOptions.ServiceAccountName = serviceAccountName

	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions() (end)", "execution", execution)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions() (end)", "options", jobOptions)
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions() (end)", "initImage", initImage)
	
	log.DefaultLogger.Infow("MULTITENANCY JobExecutor::NewJobOptions() (end)", "jobOptions.JobTemplate", jobOptions.JobTemplate)

	return
}
