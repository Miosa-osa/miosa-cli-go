package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	cliClient "github.com/Miosa-osa/miosa-cli-go/internal/client"
)

func newCatalogCmd() *cobra.Command {
	var product string
	var templateID string
	var size string
	var state string

	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Show canonical compute products, templates, sizes, and readiness",
		Long: `Show the canonical MIOSA compute catalog.

Use this before creating resources so you can choose the right product lane
(sandbox, computer, docker_deploy_host), template, size, and fast-readiness
state. Readiness states are fast_ready, cold_boot_only, and missing.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCatalog(cmd, catalogFilters{
				product:    product,
				templateID: templateID,
				size:       size,
				state:      state,
			})
		},
	}

	cmd.Flags().StringVar(&product, "product", "", "Filter by product id: sandbox, computer, docker_deploy_host")
	cmd.Flags().StringVar(&templateID, "template", "", "Filter by template id, e.g. nextjs or miosa-desktop")
	cmd.Flags().StringVar(&size, "size", "", "Filter by size: xs, small, medium, large, xl")
	cmd.Flags().StringVar(&state, "state", "", "Filter by readiness: fast_ready, cold_boot_only, missing")
	return cmd
}

type catalogFilters struct {
	product    string
	templateID string
	size       string
	state      string
}

type catalogRow struct {
	Product       string `json:"product"`
	Template      string `json:"template"`
	Size          string `json:"size"`
	State         string `json:"state"`
	ReadyHosts    int    `json:"ready_hosts"`
	CheckedHosts  int    `json:"checked_hosts"`
	ColdBootHosts int    `json:"cold_boot_hosts"`
	MissingHosts  int    `json:"missing_hosts"`
}

func runCatalog(cmd *cobra.Command, filters catalogFilters) error {
	p := printerFor(cmd)

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	catalog, err := c.ComputeCatalog(cmd.Context())
	if err != nil {
		return die(err)
	}

	rows := flattenCatalog(catalog, filters)
	if isJSON() {
		return p.JSON(map[string]interface{}{
			"data": rows,
			"meta": map[string]interface{}{
				"generated_at": catalog.GeneratedAt,
				"total":        len(rows),
			},
		})
	}

	if len(rows) == 0 {
		p.Line("No catalog entries matched.")
		return nil
	}

	table := make([][]string, 0, len(rows))
	for _, row := range rows {
		table = append(table, []string{
			row.Product,
			row.Template,
			row.Size,
			row.State,
			fmt.Sprintf("%d/%d", row.ReadyHosts, row.CheckedHosts),
			fmt.Sprintf("%d", row.ColdBootHosts),
			fmt.Sprintf("%d", row.MissingHosts),
		})
	}
	p.Table([]string{"PRODUCT", "TEMPLATE", "SIZE", "STATE", "READY", "COLD", "MISSING"}, table)
	return nil
}

func flattenCatalog(catalog *cliClient.ComputeCatalog, filters catalogFilters) []catalogRow {
	if catalog == nil {
		return nil
	}
	var rows []catalogRow
	for _, product := range catalog.Products {
		productID := firstNonEmpty(product.ID, product.ProductID)
		if !matchesFilter(productID, filters.product) {
			continue
		}
		for _, tmpl := range product.Templates {
			templateID := firstNonEmpty(tmpl.ID, tmpl.TemplateID)
			if !matchesFilter(templateID, filters.templateID) {
				continue
			}
			for _, readiness := range tmpl.ArtifactReadiness {
				if !matchesFilter(readiness.Size, filters.size) || !matchesFilter(readiness.State, filters.state) {
					continue
				}
				rows = append(rows, catalogRow{
					Product:       productID,
					Template:      templateID,
					Size:          readiness.Size,
					State:         readiness.State,
					ReadyHosts:    firstNonZero(readiness.ReadyNodes, readiness.ReadyHosts),
					CheckedHosts:  firstNonZero(readiness.CheckedNodes, readiness.CheckedHosts),
					ColdBootHosts: firstNonZero(readiness.ColdBootNodes, readiness.ColdBootHosts),
					MissingHosts:  firstNonZero(readiness.MissingNodes, readiness.MissingHosts),
				})
			}
		}
	}
	return rows
}

func matchesFilter(value, filter string) bool {
	filter = strings.TrimSpace(strings.ToLower(filter))
	if filter == "" {
		return true
	}
	return strings.ToLower(value) == filter
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
