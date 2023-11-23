# Prometheus alerts migrator
This tool serves as a Kubernetes controller that automates the migration of Prometheus alert rules to Logz.io's alert format, facilitating monitoring and alert management in a Logz.io integrated environment.

## Prerequisites
Before running this software, ensure you have:
- Go (version 1.x)
- Docker (for containerization)
- Access to a Kubernetes cluster
- Logz.io account with API access

## Configuration

Configure the application using the following environment variables:

| Environment Variable   | Description                                                                        | Default Value              |
|------------------------|------------------------------------------------------------------------------------|----------------------------|
| `LOGZIO_API_TOKEN`     | The API token for your Logz.io account.                                            | `None`                     |
| `LOGZIO_API_URL`       | The URL endpoint for the Logz.io API.                                              | `https://api.logz.io`      |
| `CONFIGMAP_ANNOTATION` | The specific annotation the controller should look for in Prometheus alert rules.  | `prometheus.io/kube-rules` |
| `RULES_DS`             | The metrics data source name in logz.io for the Prometheus rules.                  | `None`                     |
| `ENV_ID`               | Environment identifier, usually cluster name.                                      | `my-env`                   |
| `WORKER_COUNT`         | The number of workers to process the alerts.                                       | `2`                        |

Please ensure to set all necessary environment variables before running the application.

## Getting started
To start using the controller:

1. Clone the repository.
2. Navigate to the project directory.
3. Run the controller `make run-local`.

### ConfigMap Format
The controller is designed to process ConfigMaps containing Prometheus alert rules. These ConfigMaps must be annotated with a specific key that matches the value of the `ANNOTATION` environment variable for the controller to process them.

### Example ConfigMap

Below is an example of how a ConfigMap should be structured:

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

## Changelog
- v1.0.0
  - initial release 
- v1.0.1
  - Update `logzio_terraform_client`: `1.18.0` -> `1.19.0`
  - Use data source uid instead of name