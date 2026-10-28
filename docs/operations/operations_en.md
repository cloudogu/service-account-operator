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


## Migration from Dogu V2

When a consumer Dogu is migrated from Dogu V2 to V3, the Service Account Operator takes over its existing service
accounts instead of requesting new ones from the producer. The source is the **migration secret** `<doguname>-migration`
in the namespace of the SARE. It is created and deleted by the Dogu Operator; the Service Account Operator only reads it.

### Trigger

The operator takes over the credentials from the migration secret if all of the following conditions are met:

- The SARE has the label `k8s.cloudogu.com/dogu.name`. Its value determines the name of the migration secret.
- **No** service account secret exists for the SARE yet.
- The matching SAPR exists.
- The migration secret exists and contains at least one key with the SARE's prefix `<spec.producer>_`.

If any of these conditions is not met, the operator requests the credentials from the producer as usual. An existing
service account secret is never overwritten from the migration secret.

### Result

On success, the operator writes the service account secret from the data of the migration secret **without** calling the producer.

- Condition `ServiceAccountReady=True` with reason `ServiceAccountMigrated`
- `status.secretRef` points to the service account secret
- `status.lastRotation` is not set, because a migration is not a rotation
- Event of type `Normal` with action `ServiceAccountMigrated`; the note names the consumer and the migration secret

### Error behavior

If the migration secret lacks a key that the SAPR declares in `spec.http.return`:

- no service account secret is written and the producer is not called, not even as a fallback,
- the SARE gets the condition `ServiceAccountReady=False` with reason `MigrationFailed`
  - the message names the migration secret and the missing keys (e.g. `nexus_repository`),
- an event of type `Warning` with action `MigrationFailed` is emitted,
- the reconcile is retried with backoff.

Other errors while reading the migration secret lead to reason `ServiceAccountFailed` and are also retried with backoff.

### Requirement for producers

The Service Account Operator does not call the producer during a migration and afterwards treats migrated service accounts like all others.
It requests service accounts from the producer under the SARE's `spec.consumer` (e.g. `baseline`), which is usually the same name as in Dogu V2.
Producers must therefore keep their V2 accounts under this name.

Otherwise, the next reconcile with an existing service account secret (e.g. after a change of the SARE spec or a restart of the operator) sends a request for an account the producer does not know.
The producer creates a new account, and the operator overwrites the migrated credentials with it.
The old V2 account remains orphaned at the producer and is not removed when the SARE is deleted.
