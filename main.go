package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/containers/azcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
)

type Registry struct {
	Name          string
	LoginServer   string
	ResourceGroup string
	ResourceID    string
	Repos         []Repo
}

type Repo struct {
	Name string
	Tags []string
}

func filterRepos(repos []Repo, filter string) []Repo {
	if filter == "all" {
		return repos
	}
	for _, r := range repos {
		if r.Name == filter {
			return []Repo{r}
		}
	}
	return nil
}

// repoTagCounts returns map[repoName]map[tag]registryCount across all registries.
func repoTagCounts(registries []Registry, repoFilter string) map[string]map[string]int {
	counts := make(map[string]map[string]int)
	for _, reg := range registries {
		for _, repo := range filterRepos(reg.Repos, repoFilter) {
			if counts[repo.Name] == nil {
				counts[repo.Name] = make(map[string]int)
			}
			for _, tag := range repo.Tags {
				counts[repo.Name][tag]++
			}
		}
	}
	return counts
}

func printList(registries []Registry, repoFilter string) {
	for _, reg := range registries {
		repos := filterRepos(reg.Repos, repoFilter)
		if len(repos) == 0 {
			continue
		}
		fmt.Println(reg.Name)
		for _, repo := range repos {
			fmt.Printf("  %s (%d tags)\n", repo.Name, len(repo.Tags))
			for _, tag := range repo.Tags {
				fmt.Printf("    %s\n", tag)
			}
		}
	}
}

func printCommonTags(registries []Registry, repoFilter string) {
	counts := repoTagCounts(registries, repoFilter)
	for repoName, tagMap := range counts {
		var common []string
		for tag, count := range tagMap {
			if count == len(registries) {
				common = append(common, tag)
			}
		}
		if len(common) == 0 {
			continue
		}
		fmt.Println(repoName)
		for _, tag := range common {
			fmt.Printf("  %s\n", tag)
		}
	}
}

func printUniqueTags(registries []Registry, repoFilter string) {
	counts := repoTagCounts(registries, repoFilter)
	for _, reg := range registries {
		repos := filterRepos(reg.Repos, repoFilter)
		var printed bool
		for _, repo := range repos {
			var unique []string
			for _, tag := range repo.Tags {
				if counts[repo.Name][tag] < len(registries) {
					unique = append(unique, tag)
				}
			}
			if len(unique) == 0 {
				continue
			}
			if !printed {
				fmt.Println(reg.Name)
				printed = true
			}
			fmt.Printf("  %s\n", repo.Name)
			for _, tag := range unique {
				fmt.Printf("    %s\n", tag)
			}
		}
	}
}

// populateRepos fetches all repositories and their tags for the given registry.
func populateRepos(ctx context.Context, cred *azidentity.DefaultAzureCredential, reg *Registry) error {
	client, err := azcontainerregistry.NewClient("https://"+reg.LoginServer, cred, nil)
	if err != nil {
		return fmt.Errorf("failed to create client for %s: %w", reg.Name, err)
	}
	repoPager := client.NewListRepositoriesPager(nil)
	for repoPager.More() {
		page, err := repoPager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("failed to list repositories for %s: %w", reg.Name, err)
		}
		for _, name := range page.Names {
			repo := Repo{Name: *name}
			tagPager := client.NewListTagsPager(*name, nil)
			for tagPager.More() {
				tagPage, err := tagPager.NextPage(ctx)
				if err != nil {
					return fmt.Errorf("failed to list tags for %s/%s: %w", reg.Name, *name, err)
				}
				for _, tag := range tagPage.Tags {
					repo.Tags = append(repo.Tags, *tag.Name)
				}
			}
			reg.Repos = append(reg.Repos, repo)
		}
	}
	return nil
}

// findRegistryByName looks up a registry by name in the subscription and returns a Registry
// with Name, LoginServer, and ResourceGroup populated.
func findRegistryByName(ctx context.Context, armClient *armcontainerregistry.RegistriesClient, name string) (Registry, error) {
	pager := armClient.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return Registry{}, fmt.Errorf("failed to list registries: %w", err)
		}
		for _, r := range page.Value {
			if strings.EqualFold(*r.Name, name) {
				parts := strings.Split(*r.ID, "/")
				rg := ""
				if len(parts) > 4 {
					rg = parts[4]
				}
				return Registry{
					Name:          *r.Name,
					LoginServer:   *r.Properties.LoginServer,
					ResourceGroup: rg,
					ResourceID:    *r.ID,
				}, nil
			}
		}
	}
	return Registry{}, fmt.Errorf("registry %q not found in subscription", name)
}

// performSync imports images present in source but missing in target.
func performSync(ctx context.Context, armClient *armcontainerregistry.RegistriesClient, source, target Registry, repoFilter string, dryRun bool) error {
	targetIndex := make(map[string]map[string]bool)
	for _, repo := range target.Repos {
		targetIndex[repo.Name] = make(map[string]bool)
		for _, tag := range repo.Tags {
			targetIndex[repo.Name][tag] = true
		}
	}

	var count int
	noForce := armcontainerregistry.ImportModeNoForce
	for _, repo := range filterRepos(source.Repos, repoFilter) {
		for _, tag := range repo.Tags {
			if targetIndex[repo.Name][tag] {
				continue
			}
			ref := repo.Name + ":" + tag
			if dryRun {
				fmt.Printf("[dry-run] would import %s/%s -> %s/%s\n", source.Name, ref, target.Name, ref)
				count++
				continue
			}
			fmt.Printf("importing %s/%s -> %s/%s\n", source.Name, ref, target.Name, ref)
			poller, err := armClient.BeginImportImage(ctx, target.ResourceGroup, target.Name, armcontainerregistry.ImportImageParameters{
				Source: &armcontainerregistry.ImportSource{
					ResourceID:  &source.ResourceID,
					SourceImage: &ref,
				},
				TargetTags: []*string{&ref},
				Mode:       &noForce,
			}, nil)
			if err != nil {
				return fmt.Errorf("failed to start import of %s: %w", ref, err)
			}
			if _, err = poller.PollUntilDone(ctx, nil); err != nil {
				return fmt.Errorf("failed to import %s: %w", ref, err)
			}
			count++
		}
	}
	if count == 0 {
		fmt.Println("nothing to sync")
	} else if dryRun {
		fmt.Printf("%d image(s) would be imported\n", count)
	} else {
		fmt.Printf("%d image(s) imported\n", count)
	}
	return nil
}

func main() {
	subscription := flag.String("subscription", "", "Azure subscription ID or name")
	action := flag.String("action", "", "Action to perform: list, common-tags, unique-tags, sync")
	repository := flag.String("repository", "all", "Repository to filter by, or 'all'")
	registry := flag.String("registry", "", "Comma-separated registry names; omit to discover all in the subscription")
	sourceRegistry := flag.String("source-registry", "", "Source registry name (required for sync)")
	targetRegistry := flag.String("target-registry", "", "Target registry name (required for sync)")
	dryRun := flag.Bool("dry-run", false, "Print what would be imported without making changes (sync only)")
	flag.Parse()

	if *subscription == "" {
		fmt.Fprintln(os.Stderr, "error: --subscription is required")
		os.Exit(1)
	}
	if *action != "list" && *action != "common-tags" && *action != "unique-tags" && *action != "sync" {
		fmt.Fprintln(os.Stderr, "error: --action must be one of: list, common-tags, unique-tags, sync")
		os.Exit(1)
	}
	if *action == "sync" && (*sourceRegistry == "" || *targetRegistry == "") {
		fmt.Fprintln(os.Stderr, "error: --source-registry and --target-registry are required for sync")
		os.Exit(1)
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to obtain credentials: %v\n", err)
		os.Exit(1)
	}

	armClient, err := armcontainerregistry.NewRegistriesClient(*subscription, cred, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create registries client: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()
	var registries []Registry

	switch *action {
	case "sync":
		source, err := findRegistryByName(ctx, armClient, *sourceRegistry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		target, err := findRegistryByName(ctx, armClient, *targetRegistry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		registries = []Registry{source, target}
	default:
		if *registry != "" {
			for _, name := range strings.Split(*registry, ",") {
				if name = strings.TrimSpace(name); name != "" {
					registries = append(registries, Registry{Name: name, LoginServer: name + ".azurecr.io"})
				}
			}
		} else {
			pager := armClient.NewListPager(nil)
			for pager.More() {
				page, err := pager.NextPage(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: failed to list registries: %v\n", err)
					os.Exit(1)
				}
				for _, r := range page.Value {
					registries = append(registries, Registry{
						Name:        *r.Name,
						LoginServer: *r.Properties.LoginServer,
					})
				}
			}
		}
		if len(registries) == 0 {
			fmt.Fprintln(os.Stderr, "error: no registries found")
			os.Exit(1)
		}
		if len(registries) == 1 && (*action == "common-tags" || *action == "unique-tags") {
			fmt.Fprintf(os.Stderr, "error: %q requires at least 2 registries, got 1 (%s)\n", *action, registries[0].Name)
			os.Exit(1)
		}
	}

	for i := range registries {
		if err := populateRepos(ctx, cred, &registries[i]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	switch *action {
	case "list":
		printList(registries, *repository)
	case "common-tags":
		printCommonTags(registries, *repository)
	case "unique-tags":
		printUniqueTags(registries, *repository)
	case "sync":
		if err := performSync(ctx, armClient, registries[0], registries[1], *repository, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
}
