package controller

import (
	"context"
	"fmt"
	"github.com/logzio/prometheus-alerts-migrator/common"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"log"
	"os"
	"testing"
	"time"
)

const testNamespace = "alert-migrator-test"

// deployConfigMaps deploys the provided ConfigMaps to the cluster
func deployConfigMaps(clientset *kubernetes.Clientset, configs ...string) error {
	for _, config := range configs {
		// Read the YAML file content
		yamlContent, err := os.ReadFile(config)
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

func cleanupLogzioAlerts(ctl Controller) {
	folderUid, err := ctl.logzioGrafanaAlertsClient.FindOrCreatePrometheusAlertsFolder()
	if err != nil {
		log.Fatalf("Failed to get logzio alerts folder uid: %v", err)
	}
	logzioAlerts, err := ctl.logzioGrafanaAlertsClient.GetLogzioGrafanaAlerts(folderUid)
	if err != nil {
		log.Fatalf("Failed to get logzio alerts: %v", err)
	}
	ctl.logzioGrafanaAlertsClient.DeleteRules(logzioAlerts)
}

// TestControllerE2E is the main function that runs the end-to-end test
func TestControllerRulesE2E(t *testing.T) {
	// Setup the test environment
	config, err := common.GetConfig()
	if err != nil {
		t.Fatalf("Failed to get Kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		t.Fatalf("Failed to create Kubernetes clientset: %v", err)
	}
	ctlConfig := common.NewConfig()
	kubeInformerFactory := informers.NewSharedInformerFactory(clientset, time.Second*30)
	// Instantiate the controller
	ctrl := NewController(clientset, kubeInformerFactory.Core().V1().ConfigMaps(), *ctlConfig)

	// defer cleanup
	defer cleanupLogzioAlerts(*ctrl)
	defer cleanupTestCluster(clientset, testNamespace, "opentelemetry-rules", "infrastructure-rules")

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
	folderUid, err := ctrl.logzioGrafanaAlertsClient.FindOrCreatePrometheusAlertsFolder()
	if err != nil {
		t.Fatalf("Failed to get logzio alerts folder uid: %v", err)
	}
	logzioAlerts, err := ctrl.logzioGrafanaAlertsClient.GetLogzioGrafanaAlerts(folderUid)
	if err != nil {
		t.Fatalf("Failed to get logzio alerts: %v", err)
	}
	t.Log("logzio alert rules:")
	for i, alert := range logzioAlerts {
		t.Logf("%d: %v", i, alert.Title)
	}
	assert.Equal(t, 14, len(logzioAlerts))

}
