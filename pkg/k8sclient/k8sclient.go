package k8sclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"time"

	"k8s.io/client-go/dynamic"

	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"github.com/kubeshop/testkube/pkg/log"
	"go.uber.org/zap"
)

const (
	apiServerDeploymentSelector = "app.kubernetes.io/name=api-server"
	operatorDeploymentSelector  = "control-plane=controller-manager"
)

// ConnectToK8s establishes a connection to the k8s and returns a *kubernetes.Clientset
func ConnectToK8s() (*kubernetes.Clientset, error) {
	var log *zap.SugaredLogger = log.DefaultLogger
	log.Infow("MULTITENANCY: ConnectToK8s")

	config, err := GetK8sClientConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

func getKubeConfig() (string) {
	kubeconfig := `
apiVersion: v1
kind: Config
clusters:
  - name: dp-gke-test
    cluster:
      certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUVMRENDQXBTZ0F3SUJBZ0lRQnkwV1hDeGpKMHQyUTdUZTI1WUprVEFOQmdrcWhraUc5dzBCQVFzRkFEQXYKTVMwd0t3WURWUVFERXlRNU4ySmxZbVZrWmkweVl6TmtMVFEwWkRZdE9ERXlPQzB4TkdKa1pUQmxNbVEyTW1FdwpJQmNOTWpJd056STRNVEl4T1RRMFdoZ1BNakExTWpBM01qQXhNekU1TkRSYU1DOHhMVEFyQmdOVkJBTVRKRGszClltVmlaV1JtTFRKak0yUXRORFJrTmkwNE1USTRMVEUwWW1SbE1HVXlaRFl5WVRDQ0FhSXdEUVlKS29aSWh2Y04KQVFFQkJRQURnZ0dQQURDQ0FZb0NnZ0dCQUsrWEFNL1d6TGsyYklMSFByY1p5aVVIbVoxUXFndXUxckZGNDB1Zgpaa3RHMDArWlR6ZkZuZ0VSM3gvV1JCdFk3UTdWQlR6OXRMM0pzTDhVd25PVkNFRENJZkw5VEd4MTg3Z2xMcnFSCnpPaG85WXRyNTJudHlwcGRnV3c1T0VNRzZHVHRHUzJaM3diaUxuZDFTK2ZkYXJQTHRrTnRHZEI2U2xpOGJ6WTgKbGcwRGxhaHUwdHJabHpzTzJoU0gyTlhwV3FGazJ6c2tWTHBYMzZ5YmdXSGFpa1NvVkJjaGFsTTkxOVdBdUxwMgp1S0JQc08yWnN1Mm5uQ0tPZW96MDlHV1U2dzMydWErcGZpMXlvSk45NEtiYXJhODVsTUxoZzlVVWxnMEg4cVY3CkF2Yk9lUUNOcUZvT3d6cXRHRGJWRTEyVEVVRlh1T1dHNysxaDMxTG5NMEpiWlpXT2lNbFZhUHVTL1ZvVGxyeE8KK0ZaV01tTGc0dHlUWTJKd3hLdUcyRVBIaHdmOEdueGliYmxINHJkVUNUTDZWSzlRVWpNNXhNQWE2blArbGUrUApWVXF0ekNaejJyamFNc3REd2d3WDVLTGVKNXdjL2FNaDFiaDFJZlFaTEh2Y21yZVhIdGswZ2grdjUrV25qL3dsCk9kekhBQU82dDJYS2lqaktJaXBRWERuVGpRSURBUUFCbzBJd1FEQU9CZ05WSFE4QkFmOEVCQU1DQWdRd0R3WUQKVlIwVEFRSC9CQVV3QXdFQi96QWRCZ05WSFE0RUZnUVVxUjIyQ1UzQlY1SE1MZURMaGxtQm1Tczg3d0F3RFFZSgpLb1pJaHZjTkFRRUxCUUFEZ2dHQkFEMXA5QnlGS0xiejNPVWxBZkdLQXpvUVpES01vNGpzQWhoY1NBZmptTlBRCnZ1SjZzTGtpYUxjVXNnVXo4L0RkZEhmaUlSZUpTdFZBT2l3aXJtZHAxVjJYd3BwSTN6WUZ4bmRzcnNYS2I1OGcKQWl3VGJkbWNMZW9GRE1uSWNmenUwY0RTRWN0NXQzS2IrOVlFTjJNUkljNk8zbTZtQ01FYnUvRnFQNTR6TGZqTAo3dU9uQ3luYlB2VUw1NW5LSGFyYklpeG13ejF6NmNhVWc0c1oxbkowMTZuRUV1UXVkOTY5cUdlUFRSaEhpdnlLCmJEeDZaaU5hZTdGTnRHTmtFclVwdHpqTk5QS0ZhMC9qT2lXZ3d1SUp1dWJ6M3ZwN3NnbW1EemQwMWZlU283UHcKaUs4V1hoMm8xYktlTUNMYks2emxHZy9CNDJGQXJXZytScSs0Q09BLzB4ajA3KzdWMDIrT0gzaTkyMWdyV0plVgpoaDJsaWdkQTc0N1prd0V3dHc0UEx5NHFjNjE0UHY4RnNSUjEvU01qd3U1WGw5aGpsSnJtelhLVHFlQkYxanVWCkljMS9kS1dXUjVWODRkKzAvejZjTGo2Wi9kaTVNamp6ZE5FVS9wd09NQVZOTXpkeWx2THpGM01MdU9SWUF3SDUKazRrRjJDaUtFaXZjclR3QjN0VjFYdz09Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K
      server: https://10.247.2.2
contexts:
  - name: testkube-service-account@dp-gke-test
    context:
      cluster: dp-gke-test
      namespace: klarriofc000tstkube1-testkube
      user: testkube-service-account
users:
  - name: testkube-service-account
    user:
      token: eyJhbGciOiJSUzI1NiIsImtpZCI6InhpN1JYaEM4X1lKa0FuMVVLZkE5VWZucnpuZ3Z1SERUNjJzX2pBZUNfQm8ifQ.eyJpc3MiOiJrdWJlcm5ldGVzL3NlcnZpY2VhY2NvdW50Iiwia3ViZXJuZXRlcy5pby9zZXJ2aWNlYWNjb3VudC9uYW1lc3BhY2UiOiJrbGFycmlvZmMwMDB0c3RrdWJlMS10ZXN0a3ViZSIsImt1YmVybmV0ZXMuaW8vc2VydmljZWFjY291bnQvc2VjcmV0Lm5hbWUiOiJ0ZXN0a3ViZS1zZXJ2aWNlLWFjY291bnQtdG9rZW4tN2c2OGoiLCJrdWJlcm5ldGVzLmlvL3NlcnZpY2VhY2NvdW50L3NlcnZpY2UtYWNjb3VudC5uYW1lIjoidGVzdGt1YmUtc2VydmljZS1hY2NvdW50Iiwia3ViZXJuZXRlcy5pby9zZXJ2aWNlYWNjb3VudC9zZXJ2aWNlLWFjY291bnQudWlkIjoiZGNkMjdmYzQtYjhlMC00M2ViLWJiNzMtOTY0Yjk1MDQyMjkwIiwic3ViIjoic3lzdGVtOnNlcnZpY2VhY2NvdW50OmtsYXJyaW9mYzAwMHRzdGt1YmUxLXRlc3RrdWJlOnRlc3RrdWJlLXNlcnZpY2UtYWNjb3VudCJ9.nviWtkYZt5jAscgeQgzRLFH8fnQ6bv3UA5PlpABZ7Q7QQsoeMaOT7_jgRCTX40aA0i00h_kyWT6wLup_DOVo--VTI7W1nWZotGTp92ec4_gEWq95bzfZ-hhqJfj_axMyAnO37EUIX4g9ejRftjIQatmQKSJIpUbaQuMrxwQigbDTzRM43zvV539Zl_P5pOxCggOZZb4hzLFGmQJU7yM8mHZJ8ypTCxjS7UH1E5vnSMaJDqxcU5BnDQv14avAf8GepMF8Kd4O8Qb764ksJ2Ltlp3Lfs3QO6OpflXjzMh0Us-AOkIl85dmz6J30mRd1Si1obBw3BMR8FuJqZjakFY5vA
current-context: testkube-service-account@dp-gke-test`
	return kubeconfig
}

// ConnectToK8s establishes a connection to the k8s and returns a *kubernetes.Clientset
func ConnectToK8sExecutor() (*kubernetes.Clientset, error) {
	var log *zap.SugaredLogger = log.DefaultLogger
	log.Infow("MULTITENANCY: ConnectToK8sExecutor()")

	kubeconfig := getKubeConfig()

	config, err := GetK8sClientConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

// ConnectToK8sDynamic establishes a connection to the k8s and returns a dynamic.Interface
func ConnectToK8sDynamic() (dynamic.Interface, error) {
	config, err := GetK8sClientConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

func GetK8sClientConfig(kubeconfig ...string) (*rest.Config, error) {
	var err error
	var config *rest.Config
	k8sConfigExists := false
	homeDir, _ := os.UserHomeDir()
	cubeConfigPath := path.Join(homeDir, ".kube/config")

	if _, err = os.Stat(cubeConfigPath); err == nil {
		k8sConfigExists = true
	}

	if len(kubeconfig) > 0  {
		config, err = clientcmd.RESTConfigFromKubeConfig([]byte(kubeconfig[0]))
	} else if cfg, exists := os.LookupEnv("KUBECONFIG"); exists {
		config, err = clientcmd.BuildConfigFromFlags("", cfg)
	} else if k8sConfigExists {
		config, err = clientcmd.BuildConfigFromFlags("", cubeConfigPath)
	} else {
		config, err = rest.InClusterConfig()
		config.QPS = 40.0
		config.Burst = 400.0
	}

	if err != nil {
		return nil, err
	}

	return config, nil
}

// GetIngressAddress gets the hostname or ip address of the ingress with name.
func GetIngressAddress(clientSet kubernetes.Interface, ingressName string, namespace string) (string, error) {
	period := 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), period)
	defer cancel()

	var ingress *networkv1.Ingress
	var err error

	processDone := make(chan bool)
	go func() {
		ingressCount := 0
		for ingressCount == 0 {
			ingress, err = clientSet.NetworkingV1().Ingresses(namespace).Get(ctx, ingressName, metav1.GetOptions{})
			if err == nil {
				ingressCount = len(ingress.Status.LoadBalancer.Ingress)
			}
			time.Sleep(time.Second)
		}
		processDone <- true
	}()

	select {
	case <-ctx.Done():
		err = fmt.Errorf("Getting ingress failed with timeout(%d sec) previous err: %w.", period, err)
	case <-processDone:
	}

	if err != nil {
		return "", err
	}

	address := ingress.Status.LoadBalancer.Ingress[0].Hostname
	if len(address) == 0 {
		address = ingress.Status.LoadBalancer.Ingress[0].IP
	}

	return address, nil
}

// IsPersistentVolumeClaimBound TODO: add description.
func IsPersistentVolumeClaimBound(c kubernetes.Interface, podName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		pv, err := c.CoreV1().PersistentVolumeClaims(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		switch pv.Status.Phase {
		case corev1.ClaimBound:
			return true, nil
		case corev1.ClaimLost:
			return false, nil
		}
		return false, nil
	}
}

// IsPodRunning check if the pod in question is running state
func IsPodRunning(c kubernetes.Interface, podName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := c.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		switch pod.Status.Phase {
		case corev1.PodRunning, corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return false, nil
		}
		return false, nil
	}
}

// HasPodSucceeded custom method for checing if Pod is succeded (handles PodFailed state too)
func HasPodSucceeded(c kubernetes.Interface, podName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := c.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return true, nil
		case corev1.PodFailed:
			return false, nil
		}
		return false, nil
	}
}

// IsPodReady check if the pod in question is running state
func IsPodReady(c kubernetes.Interface, podName, namespace string) wait.ConditionFunc {
	return func() (bool, error) {
		pod, err := c.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if len(pod.Status.ContainerStatuses) == 0 {
			return false, nil
		}

		for _, c := range pod.Status.ContainerStatuses {
			if !c.Ready {
				return false, nil
			}
		}
		return true, nil
	}
}

// WaitForPodsReady wait for pods to be running with a timeout, return error
func WaitForPodsReady(k8sClient kubernetes.Interface, namespace string, instance string, timeout time.Duration) error {

	pods, err := k8sClient.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/instance=" + instance})
	if err != nil {
		return err
	}

	for _, pod := range pods.Items {
		if err := wait.PollImmediate(time.Second, timeout, IsPodRunning(k8sClient, pod.Name, namespace)); err != nil {
			return err
		}
		if err := wait.PollImmediate(time.Second, timeout, IsPodReady(k8sClient, pod.Name, namespace)); err != nil {
			return err
		}
	}
	return nil
}

// GetClusterVersion returns the current version of the Kubernetes cluster
func GetClusterVersion(k8sClient kubernetes.Interface) (string, error) {
	version, err := k8sClient.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}

	return version.String(), nil
}

// GetAPIServerLogs returns the latest logs from the API server deployment
func GetAPIServerLogs(ctx context.Context, k8sClient kubernetes.Interface, namespace string) ([]string, error) {
	return GetPodLogs(ctx, k8sClient, namespace, apiServerDeploymentSelector)
}

// GetOperatorLogs returns the logs from the operator
func GetOperatorLogs(ctx context.Context, k8sClient kubernetes.Interface, namespace string) ([]string, error) {
	return GetPodLogs(ctx, k8sClient, namespace, operatorDeploymentSelector)
}

// GetPodLogs returns logs for pods specified by the label selector
func GetPodLogs(ctx context.Context, k8sClient kubernetes.Interface, namespace string, selector string) ([]string, error) {
	pods, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return []string{}, fmt.Errorf("could not get operator pods: %w", err)
	}

	logs := []string{}

	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			podLogs, err := k8sClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
				Container: container.Name,
			}).Stream(ctx)
			if err != nil {
				return []string{}, fmt.Errorf("error in getting operator deployment: %w", err)
			}
			defer podLogs.Close()
			buf := new(bytes.Buffer)
			_, err = io.Copy(buf, podLogs)
			if err != nil {
				return []string{}, fmt.Errorf("error in copy information from podLogs to buf")
			}
			logs = append(logs, fmt.Sprintf("Pod: %s \n Logs: \n %s", pod.Name, buf.String()))
		}
	}
	return logs, nil
}
