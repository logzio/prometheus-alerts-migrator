package main

import (
	"flag"
	"github.com/logzio/prometheus-alerts-migrator/controller"
	"github.com/logzio/prometheus-alerts-migrator/pkg/signals"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/homedir"
	"k8s.io/klog"
	"log"
	"os"
	"path/filepath"
	"time"

	"k8s.io/client-go/tools/clientcmd"

	kubeinformers "k8s.io/client-go/informers"
)

var (
	// flags general
	helpFlag            = flag.Bool("help", false, "")
	configmapAnnotation = flag.String("annotation", "nordstrom.net/prometheus2Alerts", "Annotation that states that this configmap contains prometheus rules.")
	rulesPath           = flag.String("rulespath", "/rules", "Filepath where the rules from the configmap file should be written, this should correspond to a rule_files: location in your prometheus config.")
	reloadEndpoint      = flag.String("endpoint", "http://localhost:9090/-/reload/", "Endpoint of the Prometheus reset endpoint (eg: http://prometheus:9090/-/reload).")
	batchTime           = flag.Int("batchtime", 5, "Time window to batch updates (in seconds, default: 5)")
	// flags - kubeclient
	kubeconfigPath = flag.String("kubeconfig", "", "Path to kubeconfig. Required for out of cluster operation.")
	masterURL      = flag.String("master", "", "The address of the kube api server. Overrides the kubeconfig value, only require for off cluster operation.")

	clientset *kubernetes.Clientset
	lastSha   string
)

const (
	// Resync period for the kube controller loop.
	resyncPeriod = 30 * time.Minute
	// A subdomain added to the user specified domain for all services.
	serviceSubdomain = "svc"
	// A subdomain added to the user specified dmoain for all pods.
	podSubdomain = "pod"
)

// GetConfig returns a Kubernetes config
func GetConfig() (*rest.Config, error) {
	var config *rest.Config

	kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
	if _, err := os.Stat(kubeconfig); err == nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	} else {
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
	}

	return config, nil
}

func main() {
	flag.Parse()

	if *helpFlag ||
		*configmapAnnotation == "" ||
		*rulesPath == "" ||
		*reloadEndpoint == "" {
		flag.PrintDefaults()
		os.Exit(1)
	}

	log.Printf("Rule Updater starting.\n")
	log.Printf("ConfigMap annotation: %s\n", *configmapAnnotation)
	log.Printf("Rules location: %s\n", *rulesPath)

	// set up signals so we handle the first shutdown signal gracefully
	stopCh := signals.SetupSignalHandler()

	cfg, err := GetConfig()
	if err != nil {
		log.Fatalf("Error getting Kubernetes config")
	}

	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
	}

	kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Second*30)

	controller := controller.NewController(kubeClient, kubeInformerFactory.Core().V1().ConfigMaps(), configmapAnnotation, reloadEndpoint, rulesPath)

	// notice that there is no need to run Start methods in a separate goroutine. (i.e. go kubeInformerFactory.Start(stopCh)
	// Start method is non-blocking and runs all registered informers in a dedicated goroutine.
	kubeInformerFactory.Start(stopCh)

	if err = controller.Run(2, stopCh); err != nil {
		klog.Fatalf("Error running controller: %s", err.Error())
	}
}
