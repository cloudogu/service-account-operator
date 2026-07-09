# General Information About Service Accounting

Service accounts according to Dogu API v3 describe a mechanism that enables technical access across Dogus and
components. The Service Account Operator acts as a strict mediator between producer and consumer.

A **Producer** provides a Service Account Producer (SAPR) _Custom Resource_ that describes its own API for creating
access. Since every software component has different access requirements, it also describes how the access credentials
are structured. For example, one producer might return only an account name and passphrase, while another also needs
schema or URL information for addressing the access and provides that information as well.

Producers must implement a [Service Account Producer API](openapi.yaml) and provide it at runtime in the respective
software part (Dogu or component). For security reasons, this API may only be used by the Service Account Operator.

**Consumers**, on the other hand, describe their request for a service account from a specific producer and the Secret
in which this service account should be stored by the Service Account Operator.

![A DSA producer (not specified whether Dogu or component) contains a SAPR resource with the producer name "gareth".
Opposite it are a DSA consumer Dogu and a DSA consumer component, which each request a DSA with the producer name
"gareth". The Service Account Operator detects the match and creates a Secret for each DSA between consumer and
producer.](images/relationship_sare_sapr.drawio.png
"DSA relationship between different DSA consumers and one DSA producer")

## Example Resources

The [K8s samples repo](https://github.com/cloudogu/k8s-ecosystem-samples/tree/main/serviceaccount) contains examples of
what SAPR CRs (for producers) and SARE CRs (for consumers) can look like.
A consumer must follow the structure defined by the producer.
