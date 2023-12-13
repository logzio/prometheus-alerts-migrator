package main

import (
	"github.com/logzio/prometheus-alerts-migrator/common"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"github.com/logzio/prometheus-alerts-migrator/controller"
	"github.com/logzio/prometheus-alerts-migrator/pkg/signals"
)

func main() {
	config := common.NewConfig()

	klog.Info("Rule Updater starting.\n")
	klog.Infof("Rules configMap annotation: %s\n", config.RulesAnnotation)
	klog.Infof("AlertManager configMap annotation: %s\n", config.AlertManagerAnnotation)
	klog.Infof("Environment ID: %s\n", config.EnvID)
	klog.Infof("Logzio api url: %s\n", config.LogzioAPIURL)
	klog.Infof("Logzio rules data source: %s\n", config.RulesDS)
	klog.Infof("Number of workers: %d\n", config.WorkerCount)
	if config.IgnoreSlackText == true {
		klog.Info("Slack text field will be ignored")
	}
	if config.IgnoreSlackTitle == true {
		klog.Info("Slack title field will be ignored")
	}

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := common.GetConfig()
	if err != nil {
		klog.Fatalf("Error getting Kubernetes config: %s", err)
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err)
	}

	kubeInformerFactory := informers.NewSharedInformerFactory(kubeClient, time.Second*30)
	ctl := controller.NewController(kubeClient, kubeInformerFactory.Core().V1().ConfigMaps(), *config)
	if ctl == nil {
		klog.Fatal("Error creating controller")
	}
	kubeInformerFactory.Start(stopCh)

	if err = ctl.Run(config.WorkerCount, stopCh); err != nil {
		klog.Fatalf("Error running controller: %s", err)
	}
}
