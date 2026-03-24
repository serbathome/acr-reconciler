# acr-reconciler

A CLI tool for inspecting and comparing the contents of Azure Container Registries (ACR). It can list all repositories and tags across one or more registries, identify which images are common to every registry, and surface images that exist in only a subset of registries.

## Prerequisites

- Go 1.24.1+
- An Azure identity that can authenticate via [`DefaultAzureCredential`](https://learn.microsoft.com/azure/developer/go/azure-sdk-authentication) (e.g. `az login`, a managed identity, or environment variables)
- The identity needs at least `AcrPull` on each registry and `Reader` on the subscription (only when auto-discovering registries)

## Build

```bash
go build -o acr-reconciler .
```

## Usage

```
./acr-reconciler --subscription <id> --action <action> [--registry <names>] [--repository <name>]
```

### Flags

| Flag | Required | Default | Description |
|---|---|---|---|
| `--subscription` | yes | — | Azure subscription ID or display name |
| `--action` | yes | — | One of `list`, `common-tags`, `unique-tags` |
| `--registry` | no | — | Comma-separated registry names (e.g. `reg1,reg2`). When omitted, all registries in the subscription are discovered automatically via the ARM API. |
| `--repository` | no | `all` | Filter output to a single repository (exact name match, e.g. `argoproj/argocd`). Use `all` to include every repository. |

## Scenarios

### List all repositories and tags

Enumerates every registry in the subscription and prints all repositories with their tags.

```bash
./acr-reconciler --subscription <sub-id> --action list
```

Filter to a specific repository:

```bash
./acr-reconciler --subscription <sub-id> --action list --repository argoproj/argocd
```

### Find images present in every registry (`common-tags`)

Prints repositories and tags that exist in **all** registries. Useful for verifying that a promotion pipeline has propagated images everywhere.

```bash
./acr-reconciler --subscription <sub-id> --action common-tags
```

Target specific registries instead of discovering the whole subscription:

```bash
./acr-reconciler --subscription <sub-id> --action common-tags --registry reg1,reg2,reg3
```

### Find images missing from some registries (`unique-tags`)

Prints repositories and tags that are **not** present in all registries, grouped by registry. Helps identify drift between registries.

```bash
./acr-reconciler --subscription <sub-id> --action unique-tags
```

Narrow to a single repository:

```bash
./acr-reconciler --subscription <sub-id> --action unique-tags --repository argoproj/argocd
```

> **Note:** `common-tags` and `unique-tags` require at least 2 registries. Providing a single registry (or having only one discovered) will exit with an error.

## Technical Stack

| Component | Package |
|---|---|
| Azure identity | `github.com/Azure/azure-sdk-for-go/sdk/azidentity` |
| ARM / control-plane | `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry` |
| Data-plane (repos & tags) | `github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry` |

Two separate SDK clients are required: the ARM client discovers registries via the Azure management API, while the data-plane client connects directly to each registry endpoint (`https://<name>.azurecr.io`) to enumerate repositories and tags.

## Key Program Elements

### Types

| Type | Description |
|---|---|
| `Registry` | Holds `Name` and `LoginServer` for one registry and its slice of `Repo`. |
| `Repo` | Holds the name of a repository and a slice of tag strings. |

### Functions

| Function | Description |
|---|---|
| `main()` | Parses flags, authenticates, builds the registry list (via ARM discovery or explicit `--registry` names), fetches repositories and tags from each registry's data-plane endpoint, then dispatches to the appropriate print function. |
| `filterRepos(repos, filter)` | Returns all `Repo` entries when `filter` is `"all"`, or the single entry matching the exact name otherwise. |
| `repoTagCounts(registries, repoFilter)` | Returns a `map[repoName]map[tag]registryCount` used by both `common-tags` and `unique-tags`. |
| `printList(registries, repoFilter)` | Prints a tree of registry → repository → tags for the `list` action. |
| `printCommonTags(registries, repoFilter)` | Prints repository/tag combinations whose count equals the total number of registries. |
| `printUniqueTags(registries, repoFilter)` | Prints, per registry, the repositories and tags whose count is less than the total number of registries. |
