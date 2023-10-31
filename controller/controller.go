package controller

import (
	"context"
	"fmt"
	"github.com/logzio/logzio_terraform_client/grafana_alerts"
	"github.com/prometheus/prometheus/model/rulefmt"
	_ "github.com/prometheus/prometheus/promql/parser"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
	"math/rand"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	corev1informers "k8s.io/client-go/informers/core/v1"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

const (
	controllerAgentName = "logzio-prometheus-alerts-migrator-controller"
	ErrInvalidKey       = "InvalidKey"
	ValidKey            = "ValidKey"
	letterBytes         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterIdxBits       = 6                    // 6 bits to represent a letter index
	letterIdxMask       = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax        = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
)

// Controller is the controller implementation for Foo resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface

	configmapsLister corev1listers.ConfigMapLister
	configmapsSynced cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder

	logzioClient *grafana_alerts.GrafanaAlertClient

	resourceVersionMap         map[string]string
	interestingAnnotation      *string
	reloadEndpoint             *string
	rulesPath                  *string
	randSrc                    *rand.Source
	configmapEventRecorderFunc func(cm *corev1.ConfigMap, eventtype, reason, msg string)
}

type MultiRuleGroups struct {
	Values []rulefmt.RuleGroups
}

func NewController(
	kubeclientset *kubernetes.Clientset,
	configmapInformer corev1informers.ConfigMapInformer,
	interestingAnnotation *string,
	reloadEndpoint *string,
	rulesPath *string,
	logzioApiToken string,
	logzioApiUrl string,
) *Controller {

	utilruntime.Must(scheme.AddToScheme(scheme.Scheme))
	klog.Infof("Setting up event handlers")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	rsource := rand.NewSource(time.Now().UnixNano())
	logzioClient, _ := grafana_alerts.New(logzioApiToken, logzioApiUrl)
	controller := &Controller{
		kubeclientset:    kubeclientset,
		configmapsLister: configmapInformer.Lister(),
		configmapsSynced: configmapInformer.Informer().HasSynced,
		// TODO: update to newer version
		workqueue:             workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "configmaps"),
		recorder:              recorder,
		interestingAnnotation: interestingAnnotation,
		reloadEndpoint:        reloadEndpoint,
		rulesPath:             rulesPath,
		randSrc:               &rsource,
		resourceVersionMap:    make(map[string]string),
		logzioClient:          logzioClient,
	}

	// is this idomatic?
	controller.configmapEventRecorderFunc = controller.recordEventOnConfigMap

	klog.Info("Setting up event handlers")
	configmapInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueConfigMap,
		UpdateFunc: func(old, new interface{}) {
			newCM := new.(*corev1.ConfigMap)
			oldCM := old.(*corev1.ConfigMap)
			if newCM.ResourceVersion == oldCM.ResourceVersion {
				return
			}
			controller.enqueueConfigMap(newCM)
		},
		DeleteFunc: controller.enqueueConfigMap,
	})

	return controller
}

func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Infof("Starting %s", controllerAgentName)

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.configmapsSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Foo resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the cm resource
// with the current status of the resource.
//
// Only return errors that are transient, a return w/ an error creates a rate
// limited requeue of the resource.
func (c *Controller) syncHandler(key string) error {
	//// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the CM resource with this namespace/name
	configmap, err := c.configmapsLister.ConfigMaps(namespace).Get(name)
	if err != nil {
		// the cm may have already been deleted
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("configmap '%s' in work queue no longer exists, rebuilding rules config", key))
		} else {
			return err
		}
	}

	// current implimentation
	// 1. some configmap changed...
	// 1b. If it was nil (deleted) we have no choice but to rebuild skip to 2d1.
	// 2. does configmap have annotation
	// 2b. Get all configmaps clusterwide filter on annotation
	// 2c. Check each cm resource version against a lookup table
	// 2d. if there are any misses
	// 2d1. rebuild config

	// I don't love this bypass
	bypassCheck := false

	if configmap == nil {
		// deleted
		bypassCheck = true
	}

	if c.isRuleConfigMap(configmap) || bypassCheck {
		mapList, err := c.kubeclientset.CoreV1().ConfigMaps(corev1.NamespaceAll).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			utilruntime.HandleError(fmt.Errorf("unable to collect configmaps from the cluster; %s", err))
			return nil
		}

		if c.haveConfigMapsChanged(mapList) || bypassCheck {
			rules := c.buildFinalConfig(mapList)
			if err != nil {
				utilruntime.HandleError(err)
				return nil
			}
			if rules == nil {
				klog.Info("empty")
			}
			// TODO: list rules from logz.io alerts
			alertRules, err := c.logzioClient.ListGrafanaAlertRules()
			if err != nil {
				return err
			}
			klog.Info(alertRules)
			// TODO: compare rules against rules in logz.io alerts

			// TODO: write delta CRUD

		}

	}

	return nil
}

// get the cm on the workqueue
func (c *Controller) enqueueConfigMap(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

func (c *Controller) buildFinalConfig(mapList *corev1.ConfigMapList) *[]rulefmt.RuleNode {
	var finalRules []rulefmt.RuleNode
	for _, cm := range mapList.Items {
		if c.isRuleConfigMap(&cm) {
			cmRules := c.extractValues(&cm)
			if len(cmRules) > 0 {
				finalRules = append(finalRules, cmRules...)

			}
		}
	}
	return &finalRules
}

func (c *Controller) extractValues(cm *corev1.ConfigMap) []rulefmt.RuleNode {

	fallbackNameStub := c.createNameStub(cm)

	// make a bucket for random non fully formed rulegroups (just a single rulegroup) to live
	var mrg []rulefmt.RuleNode

	for key, value := range cm.Data {
		// try each encoding
		// try to extract a rules
		var rule rulefmt.RuleNode
		var err error
		err, rule = c.extractRules(value)
		if err != nil {
			errorMsg := fmt.Sprintf("Configmap: %s key: %s Error during extraction.", fallbackNameStub, key)
			c.configmapEventRecorderFunc(cm, corev1.EventTypeWarning, ErrInvalidKey, errorMsg)
		}
		if len(rule.Alert.Value) == 0 {
			errorMsg := fmt.Sprintf("Configmap: %s key: %s does not conform to any of the legal format Skipping.", fallbackNameStub, key)
			c.configmapEventRecorderFunc(cm, corev1.EventTypeWarning, ErrInvalidKey, errorMsg)
		} else {
			// validate the rule
			validationErrs := rule.Validate()
			if len(validationErrs) > 0 {
				for _, ruleErr := range validationErrs {
					c.configmapEventRecorderFunc(cm, corev1.EventTypeWarning, ErrInvalidKey, ruleErr.Error())
				}
				failMessage := fmt.Sprintf("Configmap: %s key: %s Rejected, no valid rules.", fallbackNameStub, key)
				c.configmapEventRecorderFunc(cm, corev1.EventTypeWarning, ErrInvalidKey, failMessage)

			} else {
				// add to the rulegroups
				mrg = append(mrg, rule)
				successMessage := fmt.Sprintf("Configmap: %s key: %s Rule accepted.", fallbackNameStub, key)
				c.configmapEventRecorderFunc(cm, corev1.EventTypeNormal, ValidKey, successMessage)
			}
		}
	}

	return mrg
}

func (c *Controller) extractRules(value string) (error, rulefmt.RuleNode) {
	rule := rulefmt.RuleNode{}
	err := yaml.Unmarshal([]byte(value), &rule)
	if err != nil {
		return err, rulefmt.RuleNode{}
	}
	if len(rule.Alert.Value) == 0 {
		return fmt.Errorf("no Rules found"), rule
	}

	return nil, rule
}

func (c *Controller) isRuleConfigMap(cm *corev1.ConfigMap) bool {
	if cm == nil {
		return false
	}
	annotations := cm.GetObjectMeta().GetAnnotations()

	for key := range annotations {
		if key == *c.interestingAnnotation {
			return true
		}
	}

	return false
}

func (c *Controller) haveConfigMapsChanged(mapList *corev1.ConfigMapList) bool {
	changes := false
	for _, cm := range mapList.Items {
		if c.isRuleConfigMap(&cm) {
			stub := c.createNameStub(&cm)
			val, ok := c.resourceVersionMap[stub]
			if !ok {
				// new configmap
				changes = true
			}
			if cm.ResourceVersion != val {
				// changed configmap
				changes = true
			}
			c.resourceVersionMap[stub] = cm.ResourceVersion
		}
	}

	return changes
}

func (c *Controller) recordEventOnConfigMap(cm *corev1.ConfigMap, eventtype, reason, msg string) {
	c.recorder.Event(cm, eventtype, reason, msg)
	if eventtype == corev1.EventTypeWarning {
		klog.Warning(msg)
	}
}

func (c *Controller) createNameStub(cm *corev1.ConfigMap) string {
	name := cm.GetObjectMeta().GetName()
	namespace := cm.GetObjectMeta().GetNamespace()

	return fmt.Sprintf("%s-%s", namespace, name)
}

func (c *Controller) saltRuleGroupNames(rgs *rulefmt.RuleGroups) *rulefmt.RuleGroups {
	usedNames := make(map[string]string)
	for i := 0; i < len(rgs.Groups); i++ {
		if _, ok := usedNames[rgs.Groups[i].Name]; ok {
			// used name, salt
			rgs.Groups[i].Name = fmt.Sprintf("%s-%s", rgs.Groups[i].Name, c.generateRandomString(5))
		}
		usedNames[rgs.Groups[i].Name] = "yes"
	}
	return rgs
}

func (c *Controller) configReload(url string) error {
	client := &http.Client{}
	req, err := http.NewRequest("POST", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Unable to reload Prometheus config: %s", err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		klog.Info("Prometheus configuration reloaded.")
		return nil
	}

	respBody, _ := ioutil.ReadAll(resp.Body)
	return fmt.Errorf("Unable to reload the Prometheus config. Endpoint: %s, Reponse StatusCode: %d, Response Body: %s", url, resp.StatusCode, string(respBody))
}

// borrowed from here https://stackoverflow.com/questions/22892120/how-to-generate-a-random-string-of-a-fixed-length-in-go
func (c *Controller) generateRandomString(n int) string {
	b := make([]byte, n)
	src := *c.randSrc
	for i, cache, remain := n-1, src.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = src.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}

	return string(b)
}
