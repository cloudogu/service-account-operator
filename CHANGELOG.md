# service-account-operator Changelog
All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v1.0.0] - 2026-07-09
**Heads-up: Breaking Change ahead**

The Producer HTTP API changed substantially from POST to PUT along with a new optional parameter

### Added
- [#7] Process updates to `ServiceAccountRequest` and `ServiceAccountProducer` resources
- [#7] Scheduled rotation of `ServiceAccountRequest` credentials

### Changed
- [#7] producer HTTP API uses PUT and may return 204 No Content
  - This includes also a new `behaviorParams` property which might be used to control the producer, allowing to add further producer targeted actions in the future
  - For usage information, please see the [OpenAPI specification](docs/operations/openapi.yaml)

## [v0.1.0] - 2026-06-19

### Added
- [#3] Reconcile `ServiceAccountRequest` resources: create service accounts via the producer HTTP API and store credentials in a Kubernetes Secret
- [#5] Deletion of `ServiceAccountRequest` resources.