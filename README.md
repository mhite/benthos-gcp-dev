# benthos-gcp-dev

Pulumi code to support bootstrapping Google Cloud Platform (GCP) environments
used during Benthos development/experiments.

You will need to add a configuration entry for the service account email
Benthos will use for its GCP identity:

```
pulumi config set benthos_service_account benthos@benthos-dev-12345.iam.gserviceaccount.com
```

You will also need to add an entry for the project where the cloud infrastructure
will be built:

```
pulumi config set gcp:project benthos-dev-12345
```