package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// groupSummary mirrors the API's group view (metadata only — a group never
// carries secret material).
type groupSummary struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	ClaimValue   *string `json:"claim_value"`
	Description  string  `json:"description"`
	MemberCount  int     `json:"member_count"`
	BindingCount int     `json:"binding_count"`
}

// resolveGroupID accepts either a group id or its unique name, so scripts can
// use whichever they have. A name is matched exactly against the group list.
func (c *apiClient) resolveGroupID(nameOrID string) (string, error) {
	var resp struct {
		Groups []groupSummary `json:"groups"`
	}
	if err := c.call("GET", "/v1/groups", nil, &resp); err != nil {
		return "", err
	}
	for _, g := range resp.Groups {
		if g.ID == nameOrID || g.Name == nameOrID {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("no group named %q", nameOrID)
}

// groupScopePath builds the group-members route for the requested scope,
// mirroring the user-members routes.
func (c *apiClient) groupScopePath(project, env string) (string, error) {
	if project == "" {
		return "/v1/instance/group-members", nil
	}
	if env == "" {
		pid, err := c.resolveProjectID(project)
		if err != nil {
			return "", err
		}
		return "/v1/projects/" + url.PathEscape(pid) + "/group-members", nil
	}
	pid, eid, err := c.resolveEnvID(project, env)
	if err != nil {
		return "", err
	}
	return "/v1/projects/" + url.PathEscape(pid) + "/environments/" + url.PathEscape(eid) + "/group-members", nil
}

func newGroupCmd() *cobra.Command {
	var address, token string
	var kind, claim, description string
	var project, env, role string
	var asJSON, yes bool

	cmd := &cobra.Command{
		Use:   "group",
		Short: "Manage groups and group role bindings",
		Long: "Groups let one binding grant a whole team a role.\n\n" +
			"A group is either 'oidc' (membership comes from the IdP's group claim at\n" +
			"login and cannot be edited here) or 'local' (an explicit member list, for\n" +
			"instances without an IdP and for password logins). A group binding can\n" +
			"never grant owner — bind an owner directly.",
	}
	cmd.PersistentFlags().StringVar(&address, "address", "", "server address")
	cmd.PersistentFlags().StringVar(&token, "token", "", "service token")

	list := &cobra.Command{
		Use: "list", Short: "List groups",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			var resp struct {
				Groups []groupSummary `json:"groups"`
			}
			if err := c.call("GET", "/v1/groups", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Groups)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tCLAIM\tMEMBERS\tBINDINGS\tID")
			for _, g := range resp.Groups {
				claimVal := "-"
				if g.ClaimValue != nil {
					claimVal = *g.ClaimValue
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n",
					g.Name, g.Kind, claimVal, g.MemberCount, g.BindingCount, g.ID)
			}
			return tw.Flush()
		},
	}
	list.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	create := &cobra.Command{
		Use: "create <name>", Short: "Create a group", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if kind != "oidc" && kind != "local" {
				return fmt.Errorf("--kind must be oidc|local")
			}
			if kind == "oidc" && claim == "" {
				return fmt.Errorf("--claim is required for an oidc group (the value your IdP emits)")
			}
			if kind == "local" && claim != "" {
				return fmt.Errorf("--claim is only meaningful for an oidc group")
			}
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[0], "kind": kind, "description": description}
			if claim != "" {
				body["claim_value"] = claim
			}
			var g groupSummary
			if err := c.call("POST", "/v1/groups", body, &g); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "created %s group %q (%s)\n", g.Kind, g.Name, g.ID)
			return nil
		},
	}
	create.Flags().StringVar(&kind, "kind", "local", "group kind: oidc|local")
	create.Flags().StringVar(&claim, "claim", "", "claim value the IdP emits (oidc groups only)")
	create.Flags().StringVar(&description, "description", "", "description")

	del := &cobra.Command{
		Use: "delete <name|id>", Short: "Delete a group (membership and bindings cascade)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			gid, err := c.resolveGroupID(args[0])
			if err != nil {
				return err
			}
			if !yes && isTerminalCmd(cmd) {
				ok, err := promptLine(cmd, fmt.Sprintf("Delete group %q and every binding it grants? [y/N]: ", args[0]))
				if err != nil {
					return err
				}
				if ok != "y" && ok != "Y" {
					return nil
				}
			}
			if err := c.call("DELETE", "/v1/groups/"+url.PathEscape(gid), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "deleted group %s\n", args[0])
			return nil
		},
	}
	del.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")

	show := &cobra.Command{
		Use: "show <name|id>", Short: "Show a group and the scopes it grants access at", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			gid, err := c.resolveGroupID(args[0])
			if err != nil {
				return err
			}
			var resp struct {
				Group    groupSummary `json:"group"`
				Bindings []struct {
					ScopeLevel string `json:"scope_level"`
					Role       string `json:"role"`
				} `json:"bindings"`
			}
			if err := c.call("GET", "/v1/groups/"+url.PathEscape(gid), nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s (%s)\n", resp.Group.Name, resp.Group.Kind)
			if resp.Group.ClaimValue != nil {
				fmt.Fprintf(out, "claim:   %s\n", *resp.Group.ClaimValue)
			}
			fmt.Fprintf(out, "members: %d\n", resp.Group.MemberCount)
			if len(resp.Bindings) == 0 {
				fmt.Fprintln(out, "grants:  none — this group confers no access")
				return nil
			}
			fmt.Fprintln(out, "grants:")
			for _, b := range resp.Bindings {
				fmt.Fprintf(out, "  %s: %s\n", b.ScopeLevel, b.Role)
			}
			return nil
		},
	}
	show.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	members := &cobra.Command{
		Use: "members <name|id>", Short: "List a group's members", Args: cobra.ExactArgs(1),
		Long: "For an oidc group this is the login-time snapshot, so it only covers\n" +
			"users who have signed in since the group was created.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			gid, err := c.resolveGroupID(args[0])
			if err != nil {
				return err
			}
			var resp struct {
				Members []struct {
					UserID    string `json:"user_id"`
					CreatedAt string `json:"created_at"`
				} `json:"members"`
			}
			if err := c.call("GET", "/v1/groups/"+url.PathEscape(gid)+"/members", nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Members)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "USER ID\tSINCE")
			for _, m := range resp.Members {
				fmt.Fprintf(tw, "%s\t%s\n", m.UserID, m.CreatedAt)
			}
			return tw.Flush()
		},
	}
	members.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	addMember := &cobra.Command{
		Use: "add-member <group> <user-id>", Short: "Add a user to a local group", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			gid, err := c.resolveGroupID(args[0])
			if err != nil {
				return err
			}
			if err := c.call("PUT", "/v1/groups/"+url.PathEscape(gid)+"/members/"+url.PathEscape(args[1]), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "added %s to %s\n", args[1], args[0])
			return nil
		},
	}

	removeMember := &cobra.Command{
		Use: "remove-member <group> <user-id>", Short: "Remove a user from a local group", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			gid, err := c.resolveGroupID(args[0])
			if err != nil {
				return err
			}
			if err := c.call("DELETE", "/v1/groups/"+url.PathEscape(gid)+"/members/"+url.PathEscape(args[1]), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "removed %s from %s\n", args[1], args[0])
			return nil
		},
	}

	bind := &cobra.Command{
		Use: "bind <group>", Short: "Grant a group a role at a scope", Args: cobra.ExactArgs(1),
		Long: "Scope defaults to the bound project/environment (see `janus setup`), or\n" +
			"pass --project/--env. With neither, the binding is instance-wide.\n\n" +
			"A group can never be granted owner.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if role == "owner" {
				return fmt.Errorf("a group cannot be granted owner — bind an owner directly")
			}
			if role != "viewer" && role != "developer" && role != "admin" {
				return fmt.Errorf("--role must be viewer|developer|admin")
			}
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			gid, err := c.resolveGroupID(args[0])
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, e, _, err := bindingValues(dir, project, env, "")
			if err != nil {
				return err
			}
			path, err := c.groupScopePath(p, e)
			if err != nil {
				return err
			}
			if err := c.call("PUT", path+"/"+url.PathEscape(gid), map[string]any{"role": role}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "bound %s as %s\n", args[0], role)
			return nil
		},
	}
	bind.Flags().StringVar(&role, "role", "", "role to grant: viewer|developer|admin")
	bind.Flags().StringVar(&project, "project", "", "project slug (omit for instance scope)")
	bind.Flags().StringVar(&env, "env", "", "environment name (omit for project scope)")
	_ = bind.MarkFlagRequired("role")

	unbind := &cobra.Command{
		Use: "unbind <group>", Short: "Revoke a group's role at a scope", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			gid, err := c.resolveGroupID(args[0])
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, e, _, err := bindingValues(dir, project, env, "")
			if err != nil {
				return err
			}
			path, err := c.groupScopePath(p, e)
			if err != nil {
				return err
			}
			if err := c.call("DELETE", path+"/"+url.PathEscape(gid), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "unbound %s\n", args[0])
			return nil
		},
	}
	unbind.Flags().StringVar(&project, "project", "", "project slug (omit for instance scope)")
	unbind.Flags().StringVar(&env, "env", "", "environment name (omit for project scope)")

	bindings := &cobra.Command{
		Use: "bindings", Short: "List the group bindings at a scope",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newAPIClient(address, token)
			if err != nil {
				return err
			}
			dir, _ := os.Getwd()
			p, e, _, err := bindingValues(dir, project, env, "")
			if err != nil {
				return err
			}
			path, err := c.groupScopePath(p, e)
			if err != nil {
				return err
			}
			var resp struct {
				Bindings []struct {
					GroupName string `json:"group_name"`
					GroupKind string `json:"group_kind"`
					Role      string `json:"role"`
				} `json:"bindings"`
			}
			if err := c.call("GET", path, nil, &resp); err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(resp.Bindings)
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "GROUP\tKIND\tROLE")
			for _, b := range resp.Bindings {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", b.GroupName, b.GroupKind, b.Role)
			}
			return tw.Flush()
		},
	}
	bindings.Flags().StringVar(&project, "project", "", "project slug (omit for instance scope)")
	bindings.Flags().StringVar(&env, "env", "", "environment name (omit for project scope)")
	bindings.Flags().BoolVar(&asJSON, "json", false, "output JSON")

	cmd.AddCommand(list, create, del, show, members, addMember, removeMember, bind, unbind, bindings)
	return cmd
}
