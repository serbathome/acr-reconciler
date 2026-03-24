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
	Name        string
	LoginServer string
	Repos       []Repo
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

func main() {
	subscription := flag.String("subscription", "", "Azure subscription ID or name")
	action := flag.String("action", "", "Action to perform: list, common-tags, unique-tags")
	repository := flag.String("repository", "all", "Repository to filter by, or 'all'")
	registry := flag.String("registry", "", "Comma-separated registry names; omit to discover all in the subscription")
	flag.Parse()

	if *subscription == "" {
		fmt.Fprintln(os.Stderr, "error: --subscription is required")
		os.Exit(1)
	}
	if *action != "list" && *action != "common-tags" && *action != "unique-tags" {
		fmt.Fprintln(os.Stderr, "error: --action must be one of: list, common-tags, unique-tags")
		os.Exit(1)
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to obtain credentials: %v\n", err)
		os.Exit(1)
	}

	var registries []Registry

	if *registry != "" {
		for _, name := range strings.Split(*registry, ",") {
			if name = strings.TrimSpace(name); name != "" {
				registries = append(registries, Registry{Name: name, LoginServer: name + ".azurecr.io"})
			}
		}
	} else {
		armClient, err := armcontainerregistry.NewRegistriesClient(*subscription, cred, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to create registries client: %v\n", err)
			os.Exit(1)
		}
		pager := armClient.NewListPager(nil)
		for pager.More() {
			page, err := pager.NextPage(context.Background())
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

	for i, reg := range registries {
		client, err := azcontainerregistry.NewClient("https://"+reg.LoginServer, cred, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to create client for %s: %v\n", reg.Name, err)
			os.Exit(1)
		}
		repoPager := client.NewListRepositoriesPager(nil)
		for repoPager.More() {
			page, err := repoPager.NextPage(context.Background())
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: failed to list repositories for %s: %v\n", reg.Name, err)
				os.Exit(1)
			}
			for _, name := range page.Names {
				repo := Repo{Name: *name}
				tagPager := client.NewListTagsPager(*name, nil)
				for tagPager.More() {
					tagPage, err := tagPager.NextPage(context.Background())
					if err != nil {
						fmt.Fprintf(os.Stderr, "error: failed to list tags for %s/%s: %v\n", reg.Name, *name, err)
						os.Exit(1)
					}
					for _, tag := range tagPage.Tags {
						repo.Tags = append(repo.Tags, *tag.Name)
					}
				}
				registries[i].Repos = append(registries[i].Repos, repo)
			}
		}
	}

	switch *action {
	case "list":
		printList(registries, *repository)
	case "common-tags":
		printCommonTags(registries, *repository)
	case "unique-tags":
		printUniqueTags(registries, *repository)
	}
}
