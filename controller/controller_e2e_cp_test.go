package controller

import (
	"github.com/logzio/prometheus-alerts-migrator/common"
	"github.com/logzio/prometheus-alerts-migrator/pkg/signals"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"log"
	"testing"
	"time"
)

var stopCh = signals.SetupSignalHandler()

func cleanupLogzioContactPoints(ctl Controller) {
	contactPoints, err := ctl.logzioGrafanaAlertsClient.GetLogzioManagedGrafanaContactPoints()
	if err != nil {
		log.Fatalf("Failed to get logzio contact points: %v", err)
	}
	ctl.logzioGrafanaAlertsClient.DeleteContactPoints(contactPoints)
}

// TestControllerE2E is the main function that runs the end-to-end test
func TestControllerContactPointsE2E(t *testing.T) {
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
	// set up signals so we handle the first shutdown signal gracefully
	// Instantiate the controller
	ctrl := NewController(clientset, kubeInformerFactory.Core().V1().ConfigMaps(), *ctlConfig)

	kubeInformerFactory.Start(stopCh)

	// test contact points
	defer cleanupLogzioContactPoints(*ctrl)
	defer cleanupTestCluster(clientset, testNamespace, "alert-manager")
	err = deployConfigMaps(clientset, "../testdata/alert_manager_contact_points.yaml")
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
	logzioContactPoints, err := ctrl.logzioGrafanaAlertsClient.GetLogzioManagedGrafanaContactPoints()
	t.Log("logzio contact points:")
	for i, contactPoint := range logzioContactPoints {
		t.Logf("%d: %v", i, contactPoint.Name)
	}
	if err != nil {
		t.Fatalf("Failed to get logzio contact points: %v", err)
	}
	assert.Equal(t, 13, len(logzioContactPoints))

}
