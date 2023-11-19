package controller

import (
	"context"
	"fmt"
	"github.com/logzio/prometheus-alerts-migrator/pkg/signals"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"os"
	"testing"
	"time"
)

const testNamespace = "alert-migrator-test"

// deployConfigMaps deploys the provided ConfigMaps to the cluster
func deployConfigMaps(clientset *kubernetes.Clientset, configs ...string) error {
	for _, config := range configs {
		// Read the YAML file content
		yamlContent, err := ioutil.ReadFile(config)
		if err != nil {
			return fmt.Errorf("failed to read YAML file %s: %v", config, err)
		}

		// Decode YAML content into Kubernetes objects
		decode := scheme.Codecs.UniversalDeserializer().Decode
		obj, _, err := decode(yamlContent, nil, nil)
		if err != nil {
			return fmt.Errorf("failed to decode YAML file %s: %v", config, err)
		}

		// Cast the object to a ConfigMap
		configMap, ok := obj.(*corev1.ConfigMap)
		if !ok {
			return fmt.Errorf("decoded object is not a ConfigMap: %v", config)
		}

		// Apply the ConfigMap to the cluster
		_, err = clientset.CoreV1().ConfigMaps(configMap.Namespace).Create(context.Background(), configMap, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create ConfigMap %s: %v", configMap.Name, err)
		}
	}
	return nil
}

// cleanupTestCluster removes deployed resources from the cluster
func cleanupTestCluster(clientset *kubernetes.Clientset, namespace string, configMapNames ...string) error {
	for _, cmName := range configMapNames {
		// Delete the ConfigMap
		err := clientset.CoreV1().ConfigMaps(namespace).Delete(context.Background(), cmName, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete ConfigMap %s: %v", cmName, err)
		}
	}
	return nil
}

// TestControllerE2E is the main function that runs the end-to-end test
func TestControllerE2E(t *testing.T) {
	// Setup the test environment
	config, err := GetConfig()
	if err != nil {
		t.Fatalf("Failed to get Kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create Kubernetes clientset: %v", err)
	}
	logzioUrl := os.Getenv("LOGZIO_API_URL")
	logzioAPIToken := os.Getenv("LOGZIO_API_TOKEN")
	rulesDS := os.Getenv("RULES_DS")
	anno := os.Getenv("CONFIGMAP_ANNOTATION")
	kubeInformerFactory := informers.NewSharedInformerFactory(clientset, time.Second*30)
	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()
	// Instantiate the controller
	ctrl := NewController(clientset, kubeInformerFactory.Core().V1().ConfigMaps(), &anno, logzioAPIToken, logzioUrl, rulesDS, "integration-test")

	// defer cleanup
	defer cleanupTestCluster(clientset, testNamespace, "opentelemetry-rules", "opentelemetry-rules-2")
	kubeInformerFactory.Start(stopCh)
	err = deployConfigMaps(clientset, "../testdata/cm.yml", "../testdata/cm2.yml")
	if err != nil {
		t.Fatalf("Failed to deploy ConfigMaps: %v", err)
	}
	go func() {
		runErr := ctrl.Run(1, stopCh)
		if runErr != nil {
			t.Errorf("Failed to run controller: %v", runErr)
			return
		}
	}()
	t.Log("going to sleep")
	time.Sleep(time.Second * 10)
	folderUid, err := ctrl.findOrCreatePrometheusAlertsFolder()
	if err != nil {
		t.Fatalf("Failed to get logzio alerts folder uid: %v", err)
	}
	logzioAlerts, err := ctrl.getLogzioGrafanaAlerts(folderUid)
	if err != nil {
		t.Fatalf("Failed to get logzio alerts: %v", err)
	}
	// defer cleanup
	defer ctrl.deleteRules(logzioAlerts, folderUid)
	assert.Equal(t, 9, len(logzioAlerts))

}
