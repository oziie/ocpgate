package main

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/oziie/ocpgate/internal/registry"
)

func newClustersCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "clusters",
		Aliases: []string{"cluster"},
		Short:   "Inspect the cluster registry",
	}
	cmd.AddCommand(newClustersListCmd(a), newClustersSyncCmd(a))
	return cmd
}

func newClustersListCmd(a *app) *cobra.Command {
	var environment string
	var refresh bool

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List clusters in the registry",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := a.registryClient()
			if err != nil {
				return err
			}
			if err := a.syncRegistry(cmd.Context(), reg, refresh); err != nil {
				return err
			}

			clusters, err := reg.List()
			if err != nil {
				return err
			}
			if environment != "" {
				clusters = filterByEnvironment(clusters, environment)
			}

			if len(clusters) == 0 {
				a.warnf("no clusters in the registry; run `ocpgate clusters sync`")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "NAME\tENVIRONMENT\tREGION\tLDAP REALM\tSTATUS\tAPI ENDPOINT")
			for _, c := range clusters {
				status := "active"
				if !c.Active {
					status = "inactive"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					c.Name, c.Environment, c.Region, c.LDAPRealm, status, c.APIEndpoint)
			}
			if err := w.Flush(); err != nil {
				return err
			}

			if synced := reg.LastSynced(); !synced.IsZero() {
				a.infof("registry last synced %s ago", time.Since(synced).Round(time.Second))
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&environment, "environment", "e", "",
		fmt.Sprintf("filter by environment (%s|%s)", registry.EnvProduction, registry.EnvTest))
	cmd.Flags().BoolVar(&refresh, "refresh", false, "sync from GitLab before listing")

	return cmd
}

func newClustersSyncCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Sync the cluster registry from GitLab",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := a.registryClient()
			if err != nil {
				return err
			}
			if err := a.syncRegistry(cmd.Context(), reg, true); err != nil {
				return err
			}

			clusters, err := reg.List()
			if err != nil {
				return err
			}
			a.infof("synced %d clusters from GitLab", len(clusters))
			return nil
		},
	}
}

func filterByEnvironment(clusters []registry.ClusterEntry, environment string) []registry.ClusterEntry {
	out := make([]registry.ClusterEntry, 0, len(clusters))
	for _, c := range clusters {
		if strings.EqualFold(c.Environment, environment) {
			out = append(out, c)
		}
	}
	return out
}
