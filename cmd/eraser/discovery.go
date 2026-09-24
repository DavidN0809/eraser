package main

import (
	"fmt"

	"github.com/eraser-privacy/eraser/internal/service"
	"github.com/spf13/cobra"
)

func addDiscoveryCommands(root *cobra.Command, load func() (*service.Service, error)) {
	var broker, digest string
	var dry bool
	scan := &cobra.Command{Use: "discover", Short: "Preview a broker-specific search; execution requires the query digest", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := load()
		if err != nil {
			return err
		}
		defer svc.History.Close()
		plan, err := svc.DiscoveryPlan(broker)
		if err != nil {
			return err
		}
		if dry || digest == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "SEARCH PREVIEW ONLY\nProvider: Brave Search API\nQuery: %s\nApproval digest: %s\nThis query is disclosed to Brave only when approved. Results are candidates, not proof. No mail is sent.\n", plan.Query, plan.Fingerprint)
			return nil
		}
		n, err := svc.Discover(cmd.Context(), broker, digest)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%d candidates; review with `eraser matches`. Zero candidates does not prove absence.\n", n)
		return nil
	}}
	scan.Flags().StringVar(&broker, "broker", "", "One selected broker ID")
	scan.Flags().StringVar(&digest, "approve-sha256", "", "Digest of the exact search preview to disclose to Brave")
	scan.Flags().BoolVar(&dry, "dry-run", false, "Preview only; never contact a search provider or send mail")
	root.AddCommand(scan)
	root.AddCommand(&cobra.Command{Use: "matches", Short: "Display sensitive local discovery evidence", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := load()
		if err != nil {
			return err
		}
		defer svc.History.Close()
		rows, err := svc.History.Matches()
		if err != nil {
			return err
		}
		for _, m := range rows {
			fmt.Fprintf(cmd.OutOrStdout(), "ID: %s\nBroker: %s\nStatus: %s\nURL: %q\nTitle: %q\nSnippet: %q\n\n", m.ID, m.BrokerID, m.Status, m.URL, m.Title, m.Snippet)
		}
		return nil
	}})
	var match, decision string
	review := &cobra.Command{Use: "review-match", Short: "Confirm a candidate is you, or reject/revoke it; never sends mail", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := load()
		if err != nil {
			return err
		}
		defer svc.History.Close()
		return svc.ReviewMatch(match, decision)
	}}
	review.Flags().StringVar(&match, "id", "", "Candidate ID from matches")
	review.Flags().StringVar(&decision, "decision", "", "confirmed or rejected")
	root.AddCommand(review)
	var forgetBroker string
	forget := &cobra.Command{Use: "forget-discovery", Short: "Delete a broker's local evidence and revoke its match confirmations", RunE: func(cmd *cobra.Command, args []string) error {
		svc, err := load()
		if err != nil {
			return err
		}
		defer svc.History.Close()
		return svc.ForgetDiscovery(forgetBroker)
	}}
	forget.Flags().StringVar(&forgetBroker, "broker", "", "Broker whose evidence to delete")
	root.AddCommand(forget)
}
