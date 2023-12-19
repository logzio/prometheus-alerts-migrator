package controller

import (
	"github.com/logzio/prometheus-alerts-migrator/common"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"testing"
	"time"
)

func cleanupLogzioNotificationPolicies(ctl Controller) {
	err := ctl.logzioGrafanaAlertsClient.ResetNotificationPolicyTree()
	if err != nil {
		klog.Error(err)
	}
}

// TestControllerE2E is the main function that runs the end-to-end test
func TestControllerNotificationPoliciesE2E(t *testing.T) {
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
	// cleanup before starting the test to start in a clean env
	cleanupLogzioNotificationPolicies(*ctrl)
	cleanupLogzioContactPoints(*ctrl)
	// test contact points
	defer cleanupLogzioNotificationPolicies(*ctrl)
	defer cleanupLogzioContactPoints(*ctrl)
	defer cleanupTestCluster(clientset, testNamespace, "alert-manager-np")
	err = deployConfigMaps(clientset, "../testdata/alert_manager_notification_policies.yaml")
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
	assert.Equal(t, 8, len(logzioContactPoints))
	logzioNotificationPolicyTree, err := ctrl.logzioGrafanaAlertsClient.GetLogzioGrafanaNotificationPolicies()
	assert.Equal(t, "my-env-alert-migrator-test-alert-manager-np-default-email", logzioNotificationPolicyTree.Receiver)
	t.Log("logzio routes:")
	for i, route := range logzioNotificationPolicyTree.Routes {
		t.Logf("route %d: %v", i, route.Receiver)
	}
	assert.Equal(t, 7, len(logzioNotificationPolicyTree.Routes))
}
