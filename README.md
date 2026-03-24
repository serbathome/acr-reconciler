# acr-reconciler

A CLI tool for inspecting, comparing, and synchronising the contents of Azure Container Registries (ACR). It can list all repositories and tags across one or more registries, identify which images are common to every registry, surface images that exist in only a subset of registries, and import missing images from a source registry into a target registry.

## Prerequisites

- Go 1.24.1+
- An Azure identity that can authenticate via [`DefaultAzureCredential`](https://learn.microsoft.com/azure/developer/go/azure-sdk-authentication) (e.g. `az login`, a managed identity, or environment variables)
- The identity needs at least:
  - `Reader` on the subscription (only when auto-discovering registries)
  - `AcrPull` on each registry being read
  - `AcrPull` on the **source** registry and `Contributor` (or the built-in `AcrImportImage` role) on the **target** registry for the `sync` action

## Build

```bash
go build -o acr-reconciler .
```

## Usage

```
./acr-reconciler --subscription <id> --action <action> [flags]
```

### Flags

| Flag | Required | Default | Description |
|---|---|---|---|
| `--subscription` | yes | — | Azure subscription ID or display name |
| `--action` | yes | — | One of `list`, `common-tags`, `unique-tags`, `sync` |
| `--registry` | no | — | Comma-separated registry names (e.g. `reg1,reg2`). When omitted, all registries in the subscription are discovered automatically via the ARM API. Ignored when `--action sync` is used. |
| `--repository` | no | `all` | Filter output to a single repository (exact name match, e.g. `argoproj/argocd`). Use `all` to include every repository. |
| `--source-registry` | sync only | — | Name of the source ACR (required for `sync`). |
| `--target-registry` | sync only | — | Name of the target ACR (required for `sync`). |
| `--dry-run` | no | `false` | Print what would be imported without making any changes (only meaningful with `--action sync`). |

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

### Sync missing images between registries (`sync`)

Imports every image that is present in the source registry but absent from the target registry. Both registries must be in the same subscription. The import is performed via the ARM API using the caller's identity — no registry credentials are needed.

```bash
./acr-reconciler --subscription <sub-id> --action sync \
  --source-registry myregistry \
  --target-registry targetregistry
```

Sync only a single repository:

```bash
./acr-reconciler --subscription <sub-id> --action sync \
  --source-registry myregistry \
  --target-registry targetregistry \
  --repository argoproj/argocd
```

Preview what would be imported without making any changes:

```bash
./acr-reconciler --subscription <sub-id> --action sync \
  --source-registry myregistry \
  --target-registry targetregistry \
  --dry-run
```

## Technical Stack

| Component | Package |
|---|---|
| Azure identity | `github.com/Azure/azure-sdk-for-go/sdk/azidentity` |
| ARM / control-plane | `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry` |
| Data-plane (repos & tags) | `github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry` |

Two separate SDK clients are used: the ARM client discovers registries and drives image imports (`BeginImportImage`), while the data-plane client connects directly to each registry endpoint (`https://<name>.azurecr.io`) to enumerate repositories and tags.

## Key Program Elements

### Types

| Type | Description |
|---|---|
| `Registry` | Holds `Name`, `LoginServer`, `ResourceGroup`, `ResourceID`, and a slice of `Repo`. `ResourceGroup` and `ResourceID` are populated from the ARM resource ID and are required for the `sync` action. |
| `Repo` | Holds the name of a repository and a slice of tag strings. |

### Functions

| Function | Description |
|---|---|
| `main()` | Parses flags, authenticates, builds the registry list (via `findRegistryByName` for `sync`, or ARM discovery / explicit `--registry` names for other actions), fetches repositories and tags via `populateRepos`, then dispatches to the appropriate action. |
| `populateRepos(ctx, cred, reg)` | Fetches all repositories and their tags from the given registry's data-plane endpoint and populates `reg.Repos` in place. |
| `findRegistryByName(ctx, armClient, name)` | Looks up a registry by name in the subscription via the ARM list API and returns a `Registry` with `Name`, `LoginServer`, `ResourceGroup`, and `ResourceID` populated. |
| `performSync(ctx, armClient, source, target, repoFilter, dryRun)` | Compares source and target repositories, then imports each tag present in `source` but absent from `target` using `BeginImportImage`. When `dryRun` is `true`, only prints what would be imported. |
| `filterRepos(repos, filter)` | Returns all `Repo` entries when `filter` is `"all"`, or the single entry matching the exact name otherwise. |
| `repoTagCounts(registries, repoFilter)` | Returns a `map[repoName]map[tag]registryCount` used by both `common-tags` and `unique-tags`. |
| `printList(registries, repoFilter)` | Prints a tree of registry → repository → tags for the `list` action. |
| `printCommonTags(registries, repoFilter)` | Prints repository/tag combinations whose count equals the total number of registries. |
| `printUniqueTags(registries, repoFilter)` | Prints, per registry, the repositories and tags whose count is less than the total number of registries. |
