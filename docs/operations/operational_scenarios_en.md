# Understanding Operational Scenarios

Dogu Service Accounts (DSA), according to Dogu API v3, pursue a similar goal in the Cloudogu EcoSystem (CES) as the
service accounts of Dogu API v2, but they are extended and have a slightly different focus. In Dogu API v2, helper
containers such as databases were also Dogus, which often required a service account for securing data storage and
separating Dogus.

With Dogu API v3, this is no longer necessary for helper containers because Dogu Helm charts can create additional
containers of their own, and access to them does not require a CES-wide mechanism. However, Dogus can still
communicate with each other. Dogu API v3 service accounts (DSA) are still required for this. Despite the name, these
DSAs do not only apply to Dogus but also to CES components that want to access Dogus or other CES components via API.
Both can act as DSA consumers and/or DSA producers, which also allows circular dependencies. Dogus and components can be
installed properly to completion. Thanks to the SARE/SAPR mechanism, the Service Account Operator decouples these
dependencies.

Many different Dogus or components can request a DSA from a DSA producer, so multiple SARE CRs can correspond to one
SAPR CR. The assignment of a SARE to a SAPR remains unaffected by this. The following graphic shows how the Service
Account Operator, DSA consumer, and DSA producer relate to each other.

![A DSA producer (not specified whether Dogu or component) contains a SAPR resource with the producer name "gareth".
Opposite it are a DSA consumer Dogu and a DSA consumer component, which each request a DSA with the producer name
"gareth". The Service Account Operator detects the match and creates a Secret for each DSA between consumer and
producer.](images/relationship_sare_sapr.drawio.png
"DSA relationship between different DSA consumers and one DSA producer")

For the DSA creation/update/deletion process to complete successfully, the Dogu or component must implement the
_Service Account Producer API_. It is available as an [OpenAPI specification](openapi.yaml).

This document describes scenarios in which DSAs are created, modified, or deleted.

## Create a DSA

Unlike DSA modification, DSA creation has only one scenario. Two resources are required before a DSA can be used
successfully:

1. Existence of a ServiceAccountRequest CR (`SARE`)
   - this corresponds to a DSA consumer
2. Existence of a ServiceAccountProducer CR (`SAPR`)
   - this corresponds to a DSA producer

If the requirements from SARE and SAPR match in the respective `.spec.producer` field, the Service Account Operator
creates credentials via an API call to the DSA producer and stores them in a well-known Secret.

![A consumer deploys a SARE for an existing SAPR. The Service Account Operator detects this and sends an endpoint
request to a Service Account Producer service. This service implements the Service Account Producer API from the
operator. The service is usually a sidecar in the producer. It creates, updates, or deletes the desired data state by
using the actual application. This yields credentials (except on delete), which the API returns to the operator. The
operator writes the credential to the Secret named by the SARE and transfers ownership of the Secret to the consumer.
The consumer can now use the DSA.](images/saOperator_calls_dogu_saService.drawio.png
"Illustration of the process in which a DSA consumer deploys a SARE. The Service Account Operator creates a Secret by
using the producer")

If a SARE exists for which the Service Account Operator cannot find a SAPR, a meaningful condition is set on the SARE.
This SARE is only considered by the operator again when a corresponding SAPR is applied to the cluster.

## Modify a DSA

While creating a DSA contains only a single process, there are several different scenarios that can lead to a change of
a DSA.

### Changes in the Producer API

When the Producer API changes, the SAPR CR must be updated. These changes can include changes to the endpoint URL, the
DSA parameters, or the structure in which credentials are returned.

If this changes the DSA parameters or the credential structure at the producer, the DSA consumer must also be adjusted accordingly. See the next section.

### Changes to DSA Parameters

The SARE field `.spec.params` is used to influence data storage or permissions. For example, for a database this could
be a UTF-8 dialect; for a web server, a URL start path. How the parameters are used depends on the DSA producer.

If the DSA producer supports the parameters, changing `.spec.params` in an existing SARE triggers another reconciliation
by the Service Account Operator, during which the DSA producer has the opportunity to change the existing data storage
for the responsible DSA consumer.

It is _possible_ that the previously created Secret does not change.

Likewise, it cannot be ruled out that a successful change produces a new credential, which in turn updates the Secret.
If this changes the structure of the Secret (number of values, names of value keys, type of encryption/encoding, and so
on), the Service Account Consumer must be able to process this new structure at the same time.

The DSA consumer should react to changes of the Secret at runtime. See [Reacting to a Secret Rotation](#reacting-to-a-secret-rotation).

### Secret Rotation

In the past, v2 Dogu service accounts could not be updated. If access had leaked, it would have had to be rotated
manually in both the consumer Dogu and the producer Dogu.

To prevent this, Dogu API v3 provides for DSA credentials to be rotated. Since these are technical accounts between two
applications whose credentials no human needs to remember, these credentials can even be rotated regularly and
frequently. Standard cron expressions are used for this:

```goregexp
^(@(annually|yearly|monthly|weekly|daily|hourly)|(((\d+,)*\d+|(\d+(\/|-)\d+)|\*)\s?){5,6})$
```

This can also be triggered manually, for example, in the event of a data leak.

For a rotation, however, the DSA producer must support reissuing credentials. On success, the DSA Secret is guaranteed
to be updated, meaning the DSA consumer should react to this change. See [Reacting to a Secret Rotation](#reacting-to-a-secret-rotation).

A simple way to trigger such a Secret rotation once is to delete the named Secret. The Service Account Operator watches
for deletion of Secrets. If the deleted Secret is a DSA Secret, credential rotation is triggered at the producer. In the
time between Secret deletion and recreation, the consumer may no longer be able to access the producer with the old
Secret because the producer may already have replaced the credentials.

#### Reacting to a Secret Rotation

It is currently unclear how the DSA consumer should react to Secret changes at runtime.
For environment variables, a pod restart is required. Changes to mounted Secrets could theoretically already be detected
at runtime and the files reread.

### DSA Producer Is Uninstalled

If the DSA producer is uninstalled, all SAPR CRs are also removed. In terms of content, this corresponds to dissolving
the agreement described under [Create a DSA](#create-a-dsa). In this case, the DSA Secret is also deleted and the DSA
consumer must react to this change.

### Internal Changes During a DSA Producer Upgrade

It is possible that an upgrade of the tool acting as DSA producer requires a change to the credentials or the storage,
for example, because encryption is no longer considered secure and the data must be re-encrypted.

The producer therefore requires an update of the DSA Secret but cannot initiate it itself. To implement this
programmatically, a pseudo DSA parameter can be added to the SAPR, analogous to the section [Changes to DSA Parameters](#changes-to-dsa-parameters), to represent this change.

Alternatively, a manual rotation of the Secret can, of course, also be initiated after the update. See [Secret Rotation
](#secret-rotation).

## Delete a DSA

If a DSA consumer no longer needs a DSA from the DSA producer (for example, because of a technology change), the SARE
should be deleted during an upgrade. In terms of content, this deletion corresponds to dissolving the agreement
described under [Create a DSA](#create-a-dsa). In this case, the DSA Secret is also deleted.

The DSA producer must react to this change by deleting all credentials and application data of the affected DSA
consumer. This approach not only saves resources but also matches our understanding of data protection: deleted data
cannot be viewed or passed on without authorization.
