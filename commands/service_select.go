package commands

import (
	"strings"

	"github.com/spf13/cobra"
)

// serviceSelectCmd is `miosa service select`: point the directory's link at
// another service (Railway's `railway service`).
func serviceSelectCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "select <service>",
		Aliases: []string{"use"},
		Short:   "Point this directory's link at another service",
		Long: `Update .miosa/link.json so later commands use this service.

  miosa link cliniciq
  miosa service select API
  miosa redeploy`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			link, err := readLink()
			if err != nil {
				return usagef("%v", err)
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			services, err := listServices(cmd.Context(), c.API, link.ProjectID, link.Environment)
			if err != nil {
				return err
			}
			ref := strings.TrimSpace(args[0])
			for _, s := range services {
				if strings.EqualFold(s.Name, ref) || strings.EqualFold(s.Slug, ref) || strings.EqualFold(s.ID, ref) {
					link.Service, link.ServiceID = s.Name, s.ID
					if _, err := writeLinkFile(link); err != nil {
						return die(err)
					}
					if isJSON() {
						return p.JSON(link)
					}
					p.Success("This directory now works on %s (%s)", s.Name, shortID(s.ID))
					return nil
				}
			}
			return usagef("no service %q in %s", ref, link.Project)
		},
	}
}
