# Prometheus alerts migrator
This tool serves as a Kubernetes controller that automates the migration of Prometheus alert rules to Logz.io's alert format, facilitating monitoring and alert management in a Logz.io integrated environment.

## Prerequisites
Before running this software, ensure you have:
- Go (version 1.x)
- Docker (for containerization)
- Access to a Kubernetes cluster
- Logz.io account with API access

## Supported contact point types
- `Email`
- `Slack`
- `Pagerduty`

More types will be supported in the future, If you have a specific request please post an issue with your request

## Configuration

Configure the application using the following environment variables:

| Environment Variable                | Description                                                                                       | Default Value                     |
|-------------------------------------|---------------------------------------------------------------------------------------------------|-----------------------------------|
| `LOGZIO_API_TOKEN`                  | The API token for your Logz.io account.                                                           | `None`                            |
| `LOGZIO_API_URL`                    | The URL endpoint for the Logz.io API.                                                             | `https://api.logz.io`             |
| `RULES_CONFIGMAP_ANNOTATION`        | The specific annotation the controller should look for in Prometheus alert rules.                 | `prometheus.io/kube-rules`        |
| `ALERTMANAGER_CONFIGMAP_ANNOTATION` | The specific annotation the controller should look for in Prometheus alert manager configuration. | `prometheus.io/kube-alertmanager` |
| `RULES_DS`                          | The metrics data source name in logz.io for the Prometheus rules.                                 | `None`                            |
| `ENV_ID`                            | Environment identifier, usually cluster name.                                                     | `my-env`                          |
| `WORKER_COUNT`                      | The number of workers to process the alerts.                                                      | `2`                               |
| `IGNORE_SLACK_TEXT`                 | Ignore slack contact points `text` field.                                                         | `flase`                           |
| `IGNORE_SLACK_TITLE`                | Ignore slack contact points `title` field.                                                        | `false`                           |

Please ensure to set all necessary environment variables before running the application.

## Getting started
To start using the controller:

1. Clone the repository.
2. Navigate to the project directory.
3. Run the controller `make run-local`.

### ConfigMap format
The controller is designed to process ConfigMaps containing Prometheus alert rules and promethium alert manager configuration. These ConfigMaps must be annotated with a specific key that matches the value of the `RULES_CONFIGMAP_ANNOTATION` or `ALERTMANAGER_CONFIGMAP_ANNOTATION` environment variables for the controller to process them.

### Example rules configMap

Below is an example of how a rules configMap should be structured:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: logzio-rules
  namespace: monitoring
  annotations:
    prometheus.io/kube-rules: "true"
data:
  all_instances_down_otel_collector: |
    alert: Opentelemetry_Collector_Down
    expr: sum(up{app="opentelemetry-collector", job="kubernetes-pods"}) == 0
    for: 5m
    labels:
      team: sre
      severity: major
    annotations:
      description: "The OpenTelemetry collector has been down for more than 5 minutes."
      summary: "Instance down"
```
- Replace `prometheus.io/kube-rules` with the actual annotation you use to identify relevant ConfigMaps. The data section should contain your Prometheus alert rules in YAML format.
- Deploy the configmap to your cluster `kubectl apply -f <configmap-file>.yml`

### Example alert manager configMap

Below is an example of how a alert manager ConfigMap should be structured:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: logzio-rules
  namespace: monitoring
  annotations:
    prometheus.io/kube-alertmanager: "true"
data:
  all_instances_down_otel_collector: |
    global:
      # Global configurations, adjust these to your SMTP server details
      smtp_smarthost: 'smtp.example.com:587'
      smtp_from: 'alertmanager@example.com'
      smtp_auth_username: 'alertmanager'
      smtp_auth_password: 'password'
    # The root route on which each incoming alert enters.
    route:
      receiver: 'default-receiver'
      group_by: ['alertname', 'env']
      group_wait: 30s
      group_interval: 5m
      repeat_interval: 1h
      # Child routes
      routes:
        - match:
            env: production
          receiver: 'slack-production'
          continue: true
        - match:
            env: staging
          receiver: 'slack-staging'
          continue: true
    
    # Receivers defines ways to send notifications about alerts.
    receivers:
      - name: 'default-receiver'
        email_configs:
          - to: 'alerts@example.com'
      - name: 'slack-production'
        slack_configs:
          - api_url: 'https://hooks.slack.com/services/T00000000/B00000000/'
            channel: '#prod-alerts'
      - name: 'slack-staging'
        slack_configs:
          - api_url: 'https://hooks.slack.com/services/T00000000/B11111111/'
            channel: '#staging-alerts'

```
- Replace `prometheus.io/kube-alertmanager` with the actual annotation you use to identify relevant ConfigMaps. The data section should contain your Prometheus alert rules in YAML format.
- Deploy the configmap to your cluster `kubectl apply -f <configmap-file>.yml`


## Changelog
- v1.0.3
  - Handle Prometheus alert manager configuration file
  - Add CRUD operations for contact points and notification policies
  - Add `IGNORE_SLACK_TEXT` and `IGNORE_SLACK_TITLE` flags
- v1.0.2
  - Add `reduce` query to alerts (grafana alerts can evaluate alerts only from reduced data)
- v1.0.1
  - Update `logzio_terraform_client`: `1.18.0` -> `1.19.0`
  - Use data source uid instead of name
- v1.0.0
  - initial release 
