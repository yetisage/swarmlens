package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/swarm"
	"gopkg.in/yaml.v3"
)

// doc builds the "Key:   value" text of a describe view.
type doc struct {
	b       strings.Builder
	pending string // section header waiting for its first entry
}

const descKeyWidth = 18

// kv writes a key with its value(s). Further values continue on the
// following lines, aligned under the first. Empty values are skipped.
func (d *doc) kv(indent int, key string, vals ...string) {
	var vs []string
	for _, v := range vals {
		if v != "" {
			vs = append(vs, v)
		}
	}
	if len(vs) == 0 {
		return
	}
	d.flushHead(indent)
	pad := strings.Repeat(" ", indent)
	d.b.WriteString(pad)
	fmt.Fprintf(&d.b, "%-*s", descKeyWidth, key+":")
	d.b.WriteString(vs[0])
	d.b.WriteByte('\n')
	for _, v := range vs[1:] {
		d.b.WriteString(pad)
		d.b.WriteString(strings.Repeat(" ", descKeyWidth))
		d.b.WriteString(v)
		d.b.WriteByte('\n')
	}
}

// head starts a section. The header is only written once the section gets
// an entry, so empty sections do not leave a dangling title behind.
func (d *doc) head(indent int, name string) { d.pending = strings.Repeat(" ", indent) + name + ":\n" }

// flushHead writes a pending header when an indented entry follows it, and
// drops it when a top-level entry shows the section was empty.
func (d *doc) flushHead(indent int) {
	if d.pending != "" && indent > 0 {
		d.b.WriteString(d.pending)
	}
	d.pending = ""
}

// line writes a pre-formatted line (belonging to the current section).
func (d *doc) line(s string) {
	d.flushHead(2)
	d.b.WriteString(s)
}

func (d *doc) String() string { return d.b.String() }

func sortedPairs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("%s (%s ago)", t.Local().Format("2006-01-02 15:04:05 MST"), HumanDuration(time.Since(t)))
}

func cores(nano int64) string { return fmt.Sprintf("%.2f", float64(nano)/1e9) }

func resourceText(nano, mem int64) string {
	var parts []string
	if nano > 0 {
		parts = append(parts, "cpu "+cores(nano))
	}
	if mem > 0 {
		parts = append(parts, "memory "+HumanBytes(mem))
	}
	return strings.Join(parts, ", ")
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Describe implements Backend.
func (a *API) Describe(ctx context.Context, kind Kind, id string) (string, error) {
	switch kind {
	case KindService:
		return a.describeService(ctx, id)
	case KindTask:
		return a.describeTask(ctx, id)
	case KindNode:
		return a.describeNode(ctx, id)
	case KindNetwork:
		return a.describeNetwork(ctx, id)
	case KindVolume:
		return a.describeVolume(ctx, id)
	case KindSecret:
		return a.describeSecret(ctx, id)
	case KindConfig:
		return a.describeConfig(ctx, id)
	case KindStack:
		return a.describeStack(ctx, id)
	}
	return "", fmt.Errorf("cannot describe a %s", kind)
}

func (a *API) describeService(ctx context.Context, id string) (string, error) {
	svc, _, err := a.cli.ServiceInspectWithRaw(ctx, id, swarm.ServiceInspectOptions{})
	if err != nil {
		return "", err
	}
	listed, _ := a.cli.ServiceList(ctx, swarm.ServiceListOptions{Filters: filters.NewArgs(filters.Arg("id", svc.ID)), Status: true})
	if len(listed) == 1 {
		svc.ServiceStatus = listed[0].ServiceStatus
	}
	s := toService(svc)

	var d doc
	d.kv(0, "Name", s.Name)
	d.kv(0, "ID", svc.ID)
	d.kv(0, "Stack", s.Stack)
	d.kv(0, "Mode", s.Mode)
	switch s.Mode {
	case "replicated", "global":
		d.kv(0, "Replicas", fmt.Sprintf("%d running, %d desired", s.Running, s.Desired))
	case "replicated-job":
		d.kv(0, "Job", fmt.Sprintf("%d running, %d/%d completed", s.Running, s.Completed, s.JobTotal))
	}
	d.kv(0, "Image", s.Image)
	d.kv(0, "Ports", s.Ports...)
	d.kv(0, "Labels", orNone(sortedPairs(s.Labels))...)
	d.kv(0, "Created", stamp(s.Created))
	d.kv(0, "Updated", stamp(s.Updated))
	if svc.UpdateStatus != nil {
		us := svc.UpdateStatus
		d.kv(0, "Update Status", strings.TrimSpace(string(us.State)+" — "+us.Message))
		if us.StartedAt != nil {
			d.kv(0, "Update Started", stamp(*us.StartedAt))
		}
		if us.CompletedAt != nil {
			d.kv(0, "Update Completed", stamp(*us.CompletedAt))
		}
	}
	d.kv(0, "Rollback", map[bool]string{true: "available (previous spec kept)", false: "not available"}[svc.PreviousSpec != nil])

	tt := svc.Spec.TaskTemplate
	if cs := tt.ContainerSpec; cs != nil {
		d.head(0, "Container")
		d.kv(2, "Command", strings.Join(cs.Command, " "))
		d.kv(2, "Args", strings.Join(cs.Args, " "))
		d.kv(2, "Env", cs.Env...)
		d.kv(2, "Dir", cs.Dir)
		d.kv(2, "User", cs.User)
		d.kv(2, "Hostname", cs.Hostname)
		d.kv(2, "Labels", sortedPairs(cs.Labels)...)
		var mounts []string
		for _, m := range cs.Mounts {
			ro := ""
			if m.ReadOnly {
				ro = " (ro)"
			}
			mounts = append(mounts, fmt.Sprintf("%s %s -> %s%s", m.Type, m.Source, m.Target, ro))
		}
		d.kv(2, "Mounts", mounts...)
		var secrets []string
		for _, r := range cs.Secrets {
			t := r.SecretName
			if r.File != nil {
				t += " -> /run/secrets/" + r.File.Name
			}
			secrets = append(secrets, t)
		}
		d.kv(2, "Secrets", secrets...)
		var configs []string
		for _, r := range cs.Configs {
			t := r.ConfigName
			if r.File != nil {
				t += " -> " + r.File.Name
			}
			configs = append(configs, t)
		}
		d.kv(2, "Configs", configs...)
		if hc := cs.Healthcheck; hc != nil && len(hc.Test) > 0 {
			d.kv(2, "Healthcheck", fmt.Sprintf("%s (interval %s, timeout %s, retries %d)", strings.Join(hc.Test, " "), hc.Interval, hc.Timeout, hc.Retries))
		}
		if cs.StopGracePeriod != nil {
			d.kv(2, "Stop Grace", cs.StopGracePeriod.String())
		}
		d.kv(2, "Stop Signal", cs.StopSignal)
		if cs.TTY {
			d.kv(2, "TTY", "true")
		}
		d.kv(2, "Cap Add", strings.Join(cs.CapabilityAdd, ", "))
		d.kv(2, "Cap Drop", strings.Join(cs.CapabilityDrop, ", "))
	}
	if pl := tt.Placement; pl != nil {
		d.head(0, "Placement")
		d.kv(2, "Constraints", pl.Constraints...)
		var prefs []string
		for _, p := range pl.Preferences {
			if p.Spread != nil {
				prefs = append(prefs, "spread "+p.Spread.SpreadDescriptor)
			}
		}
		d.kv(2, "Preferences", prefs...)
		if pl.MaxReplicas > 0 {
			d.kv(2, "Max per Node", fmt.Sprint(pl.MaxReplicas))
		}
	}
	if r := tt.Resources; r != nil {
		d.head(0, "Resources")
		if r.Limits != nil {
			d.kv(2, "Limits", resourceText(r.Limits.NanoCPUs, r.Limits.MemoryBytes))
		}
		if r.Reservations != nil {
			d.kv(2, "Reservations", resourceText(r.Reservations.NanoCPUs, r.Reservations.MemoryBytes))
		}
	}
	if len(tt.Networks) > 0 {
		names := a.networkNames(ctx)
		var nets []string
		for _, n := range tt.Networks {
			name := names[n.Target]
			if name == "" {
				name = n.Target
			}
			if len(n.Aliases) > 0 {
				name += " (aliases " + strings.Join(n.Aliases, ", ") + ")"
			}
			nets = append(nets, name)
		}
		d.kv(0, "Networks", nets...)
	}
	if es := svc.Spec.EndpointSpec; es != nil {
		d.kv(0, "Endpoint Mode", string(es.Mode))
	}
	if u := svc.Spec.UpdateConfig; u != nil {
		d.head(0, "Update Config")
		writeUpdateConfig(&d, u)
	}
	if u := svc.Spec.RollbackConfig; u != nil {
		d.head(0, "Rollback Config")
		writeUpdateConfig(&d, u)
	}
	if rp := tt.RestartPolicy; rp != nil {
		d.head(0, "Restart Policy")
		d.kv(2, "Condition", string(rp.Condition))
		if rp.Delay != nil {
			d.kv(2, "Delay", rp.Delay.String())
		}
		if rp.MaxAttempts != nil {
			d.kv(2, "Max Attempts", fmt.Sprint(*rp.MaxAttempts))
		}
		if rp.Window != nil {
			d.kv(2, "Window", rp.Window.String())
		}
	}

	if tasks, err := a.Tasks(ctx, TaskQuery{ServiceID: svc.ID}); err == nil && len(tasks) > 0 {
		d.head(0, "Tasks")
		sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Created.After(tasks[j].Created) })
		for i, t := range tasks {
			if i == 12 {
				d.line(fmt.Sprintf("  … %d more\n", len(tasks)-i))
				break
			}
			line := fmt.Sprintf("%-22s %-10s %-14s %s", t.Name, t.State, t.Node, HumanDuration(time.Since(t.Updated)))
			if t.Error != "" {
				line += "  " + t.Error
			}
			d.line("  " + line + "\n")
		}
	}
	return d.String(), nil
}

func writeUpdateConfig(d *doc, u *swarm.UpdateConfig) {
	d.kv(2, "Parallelism", fmt.Sprint(u.Parallelism))
	d.kv(2, "Delay", u.Delay.String())
	d.kv(2, "Failure Action", u.FailureAction)
	d.kv(2, "Monitor", u.Monitor.String())
	d.kv(2, "Max Failure Ratio", fmt.Sprint(u.MaxFailureRatio))
	d.kv(2, "Order", u.Order)
}

func orNone(ss []string) []string {
	if len(ss) == 0 {
		return []string{"<none>"}
	}
	return ss
}

// networkNames maps network ids to names; failures yield an empty map and
// the caller falls back to ids.
func (a *API) networkNames(ctx context.Context) map[string]string {
	out := map[string]string{}
	if nets, err := a.cli.NetworkList(ctx, network.ListOptions{}); err == nil {
		for _, n := range nets {
			out[n.ID] = n.Name
		}
	}
	return out
}

func (a *API) describeTask(ctx context.Context, id string) (string, error) {
	t, _, err := a.cli.TaskInspectWithRaw(ctx, id)
	if err != nil {
		return "", err
	}
	tasks, _ := a.Tasks(ctx, TaskQuery{ServiceID: t.ServiceID})
	var row Task
	for _, r := range tasks {
		if r.ID == t.ID {
			row = r
		}
	}

	var d doc
	d.kv(0, "Name", row.Name)
	d.kv(0, "ID", t.ID)
	d.kv(0, "Service", row.ServiceName)
	d.kv(0, "Stack", row.Stack)
	d.kv(0, "Node", row.Node)
	if t.Slot > 0 {
		d.kv(0, "Slot", fmt.Sprint(t.Slot))
	}
	d.kv(0, "Image", row.Image)
	d.kv(0, "Desired State", string(t.DesiredState))
	d.kv(0, "State", fmt.Sprintf("%s (since %s ago)", t.Status.State, HumanDuration(time.Since(t.Status.Timestamp))))
	d.kv(0, "Message", t.Status.Message)
	d.kv(0, "Error", t.Status.Err)
	if cs := t.Status.ContainerStatus; cs != nil {
		d.kv(0, "Container ID", cs.ContainerID)
		if t.Status.State == swarm.TaskStateFailed || t.Status.State == swarm.TaskStateComplete || t.Status.State == swarm.TaskStateShutdown {
			d.kv(0, "Exit Code", fmt.Sprint(cs.ExitCode))
		}
	}
	var nets []string
	for _, n := range t.NetworksAttachments {
		nets = append(nets, n.Network.Spec.Name+" "+strings.Join(n.Addresses, ", "))
	}
	d.kv(0, "Networks", nets...)
	d.kv(0, "Created", stamp(t.CreatedAt))
	d.kv(0, "Updated", stamp(t.UpdatedAt))
	return d.String(), nil
}

func (a *API) describeNode(ctx context.Context, id string) (string, error) {
	n, _, err := a.cli.NodeInspectWithRaw(ctx, id)
	if err != nil {
		return "", err
	}
	var d doc
	d.kv(0, "Hostname", n.Description.Hostname)
	d.kv(0, "ID", n.ID)
	d.kv(0, "Role", string(n.Spec.Role))
	d.kv(0, "Availability", string(n.Spec.Availability))
	d.kv(0, "Status", strings.TrimSpace(string(n.Status.State)+" "+n.Status.Message))
	d.kv(0, "Address", n.Status.Addr)
	if m := n.ManagerStatus; m != nil {
		st := string(m.Reachability)
		if m.Leader {
			st = "leader"
		}
		d.kv(0, "Manager", st+" at "+m.Addr)
	}
	d.kv(0, "Engine", n.Description.Engine.EngineVersion)
	d.kv(0, "Platform", n.Description.Platform.OS+"/"+n.Description.Platform.Architecture)
	d.kv(0, "Resources", resourceText(n.Description.Resources.NanoCPUs, n.Description.Resources.MemoryBytes))
	d.kv(0, "Labels", orNone(sortedPairs(n.Spec.Labels))...)
	d.kv(0, "Engine Labels", sortedPairs(n.Description.Engine.Labels)...)
	var plugins []string
	for _, p := range n.Description.Engine.Plugins {
		plugins = append(plugins, p.Type+": "+p.Name)
	}
	d.kv(0, "Plugins", plugins...)
	d.kv(0, "Created", stamp(n.CreatedAt))
	d.kv(0, "Updated", stamp(n.UpdatedAt))

	if tasks, err := a.Tasks(ctx, TaskQuery{NodeID: n.ID}); err == nil {
		var running []Task
		for _, t := range tasks {
			if t.Desired == "running" {
				running = append(running, t)
			}
		}
		if len(running) > 0 {
			d.head(0, "Tasks")
			for i, t := range running {
				if i == 15 {
					d.line(fmt.Sprintf("  … %d more\n", len(running)-i))
					break
				}
				d.line(fmt.Sprintf("  %-26s %-10s %s\n", t.Name, t.State, HumanDuration(time.Since(t.Updated))))
			}
		}
	}
	return d.String(), nil
}

func (a *API) describeNetwork(ctx context.Context, id string) (string, error) {
	n, err := a.cli.NetworkInspect(ctx, id, network.InspectOptions{})
	if err != nil {
		return "", err
	}
	var d doc
	d.kv(0, "Name", n.Name)
	d.kv(0, "ID", n.ID)
	d.kv(0, "Stack", n.Labels[StackLabel])
	d.kv(0, "Driver", n.Driver)
	d.kv(0, "Scope", n.Scope)
	d.kv(0, "Internal", boolText(n.Internal))
	d.kv(0, "Attachable", boolText(n.Attachable))
	d.kv(0, "Ingress", boolText(n.Ingress))
	d.kv(0, "IPv6", boolText(n.EnableIPv6))
	for _, c := range n.IPAM.Config {
		d.kv(0, "Subnet", strings.TrimSpace(c.Subnet+"  gateway "+c.Gateway))
	}
	d.kv(0, "Options", sortedPairs(n.Options)...)
	d.kv(0, "Labels", orNone(sortedPairs(n.Labels))...)
	d.kv(0, "Created", stamp(n.Created))
	if len(n.Containers) > 0 {
		d.head(0, "Containers")
		var rows []string
		for _, c := range n.Containers {
			rows = append(rows, fmt.Sprintf("  %-40s %s", c.Name, c.IPv4Address))
		}
		sort.Strings(rows)
		d.line(strings.Join(rows, "\n") + "\n")
	}
	return d.String(), nil
}

func (a *API) describeVolume(ctx context.Context, id string) (string, error) {
	v, err := a.cli.VolumeInspect(ctx, id)
	if err != nil {
		return "", err
	}
	var d doc
	d.kv(0, "Name", v.Name)
	d.kv(0, "Stack", v.Labels[StackLabel])
	d.kv(0, "Driver", v.Driver)
	d.kv(0, "Scope", v.Scope)
	d.kv(0, "Mountpoint", v.Mountpoint)
	d.kv(0, "Options", sortedPairs(v.Options)...)
	d.kv(0, "Labels", orNone(sortedPairs(v.Labels))...)
	if t, err := time.Parse(time.RFC3339, v.CreatedAt); err == nil {
		d.kv(0, "Created", stamp(t))
	}
	return d.String(), nil
}

func (a *API) describeSecret(ctx context.Context, id string) (string, error) {
	s, _, err := a.cli.SecretInspectWithRaw(ctx, id)
	if err != nil {
		return "", err
	}
	var d doc
	d.kv(0, "Name", s.Spec.Name)
	d.kv(0, "ID", s.ID)
	d.kv(0, "Stack", s.Spec.Labels[StackLabel])
	if s.Spec.Driver != nil {
		d.kv(0, "Driver", s.Spec.Driver.Name)
	}
	d.kv(0, "Labels", orNone(sortedPairs(s.Spec.Labels))...)
	d.kv(0, "Created", stamp(s.CreatedAt))
	d.kv(0, "Updated", stamp(s.UpdatedAt))
	d.kv(0, "Value", "<hidden — the engine never returns secret values>")
	if used := a.usedBy(ctx, KindSecret, s.Spec.Name); len(used) > 0 {
		d.kv(0, "Used By", used...)
	}
	return d.String(), nil
}

func (a *API) describeConfig(ctx context.Context, id string) (string, error) {
	c, _, err := a.cli.ConfigInspectWithRaw(ctx, id)
	if err != nil {
		return "", err
	}
	var d doc
	d.kv(0, "Name", c.Spec.Name)
	d.kv(0, "ID", c.ID)
	d.kv(0, "Stack", c.Spec.Labels[StackLabel])
	d.kv(0, "Labels", orNone(sortedPairs(c.Spec.Labels))...)
	d.kv(0, "Created", stamp(c.CreatedAt))
	d.kv(0, "Updated", stamp(c.UpdatedAt))
	d.kv(0, "Size", HumanBytes(int64(len(c.Spec.Data))))
	if used := a.usedBy(ctx, KindConfig, c.Spec.Name); len(used) > 0 {
		d.kv(0, "Used By", used...)
	}
	if text, ok := printable(c.Spec.Data); ok {
		d.head(0, "Data")
		for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			d.line("  " + l + "\n")
		}
	} else if len(c.Spec.Data) > 0 {
		d.kv(0, "Data", "<binary>")
	}
	return d.String(), nil
}

// usedBy lists the services referencing a secret or config, so the user
// can see why deleting it would be refused.
func (a *API) usedBy(ctx context.Context, kind Kind, name string) []string {
	svcs, err := a.cli.ServiceList(ctx, swarm.ServiceListOptions{})
	if err != nil {
		return nil
	}
	var out []string
	for _, s := range svcs {
		cs := s.Spec.TaskTemplate.ContainerSpec
		if cs == nil {
			continue
		}
		switch kind {
		case KindSecret:
			for _, r := range cs.Secrets {
				if r.SecretName == name {
					out = append(out, s.Spec.Name)
					break
				}
			}
		case KindConfig:
			for _, r := range cs.Configs {
				if r.ConfigName == name {
					out = append(out, s.Spec.Name)
					break
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func (a *API) describeStack(ctx context.Context, name string) (string, error) {
	stacks, err := a.Stacks(ctx)
	if err != nil {
		return "", err
	}
	var st *Stack
	for i := range stacks {
		if stacks[i].Name == name {
			st = &stacks[i]
		}
	}
	if st == nil {
		return "", fmt.Errorf("stack %q not found", name)
	}
	var d doc
	d.kv(0, "Name", st.Name)
	d.kv(0, "Orchestrator", "Swarm")
	d.kv(0, "Services", fmt.Sprintf("%d (%d degraded)", st.Services, st.Degraded))
	d.kv(0, "Tasks", fmt.Sprintf("%d running, %d desired", st.Running, st.Desired))
	d.kv(0, "Networks", fmt.Sprint(st.Networks))
	d.kv(0, "Volumes", fmt.Sprint(st.Volumes))
	d.kv(0, "Secrets", fmt.Sprint(st.Secrets))
	d.kv(0, "Configs", fmt.Sprint(st.Configs))
	d.kv(0, "Created", stamp(st.Created))

	if svcs, err := a.Services(ctx); err == nil {
		d.head(0, "Services")
		for _, s := range svcs {
			if s.Stack == name {
				d.line(fmt.Sprintf("  %-34s %-12s %d/%d  %s\n", s.Name, s.Mode, s.Running, s.Desired, s.Image))
			}
		}
	}
	return d.String(), nil
}

// printable returns data as text when it is valid, human-readable UTF-8.
func printable(data []byte) (string, bool) {
	if len(data) == 0 || !utf8.Valid(data) {
		return "", false
	}
	for _, r := range string(data) {
		if !unicode.IsPrint(r) && r != '\n' && r != '\t' && r != '\r' {
			return "", false
		}
	}
	return string(data), true
}

// YAML implements Backend: the engine's own inspect document as YAML.
func (a *API) YAML(ctx context.Context, kind Kind, id string) (string, error) {
	var raw []byte
	var err error
	switch kind {
	case KindService:
		_, raw, err = a.cli.ServiceInspectWithRaw(ctx, id, swarm.ServiceInspectOptions{})
	case KindTask:
		_, raw, err = a.cli.TaskInspectWithRaw(ctx, id)
	case KindNode:
		_, raw, err = a.cli.NodeInspectWithRaw(ctx, id)
	case KindNetwork:
		_, raw, err = a.cli.NetworkInspectWithRaw(ctx, id, network.InspectOptions{})
	case KindVolume:
		_, raw, err = a.cli.VolumeInspectWithRaw(ctx, id)
	case KindSecret:
		_, raw, err = a.cli.SecretInspectWithRaw(ctx, id)
	case KindConfig:
		_, raw, err = a.cli.ConfigInspectWithRaw(ctx, id)
		if err == nil {
			return configYAML(raw)
		}
	case KindStack:
		return a.stackYAML(ctx, id)
	default:
		return "", fmt.Errorf("no YAML for a %s", kind)
	}
	if err != nil {
		return "", err
	}
	return JSONToYAML(raw)
}

// configYAML renders a config with its data decoded, since a base64 blob is
// of no use to a human.
func configYAML(raw []byte) (string, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(raw, &n); err != nil {
		return "", err
	}
	if len(n.Content) > 0 {
		root := n.Content[0]
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value != "Spec" {
				continue
			}
			spec := root.Content[i+1]
			for j := 0; j+1 < len(spec.Content); j += 2 {
				if spec.Content[j].Value != "Data" {
					continue
				}
				var data []byte
				if err := json.Unmarshal([]byte(`"`+spec.Content[j+1].Value+`"`), &data); err == nil {
					if text, ok := printable(data); ok {
						spec.Content[j+1].Value = text
						spec.Content[j+1].Tag = "!!str"
					}
				}
			}
		}
	}
	clearStyle(&n)
	out, err := yaml.Marshal(&n)
	return string(out), err
}

func (a *API) stackYAML(ctx context.Context, name string) (string, error) {
	type item struct {
		Name     string `json:"name"`
		Mode     string `json:"mode,omitempty"`
		Replicas string `json:"replicas,omitempty"`
		Image    string `json:"image,omitempty"`
	}
	out := struct {
		Name     string   `json:"name"`
		Services []item   `json:"services"`
		Networks []string `json:"networks"`
		Volumes  []string `json:"volumes"`
		Secrets  []string `json:"secrets"`
		Configs  []string `json:"configs"`
	}{Name: name, Services: []item{}, Networks: []string{}, Volumes: []string{}, Secrets: []string{}, Configs: []string{}}

	svcs, err := a.Services(ctx)
	if err != nil {
		return "", err
	}
	for _, s := range svcs {
		if s.Stack == name {
			out.Services = append(out.Services, item{Name: s.Name, Mode: s.Mode, Replicas: fmt.Sprintf("%d/%d", s.Running, s.Desired), Image: s.Image})
		}
	}
	if len(out.Services) == 0 {
		return "", fmt.Errorf("stack %q not found", name)
	}
	if v, err := a.Networks(ctx); err == nil {
		for _, x := range v {
			if x.Stack == name {
				out.Networks = append(out.Networks, x.Name)
			}
		}
	}
	if v, err := a.Volumes(ctx); err == nil {
		for _, x := range v {
			if x.Stack == name {
				out.Volumes = append(out.Volumes, x.Name)
			}
		}
	}
	if v, err := a.Secrets(ctx); err == nil {
		for _, x := range v {
			if x.Stack == name {
				out.Secrets = append(out.Secrets, x.Name)
			}
		}
	}
	if v, err := a.Configs(ctx); err == nil {
		for _, x := range v {
			if x.Stack == name {
				out.Configs = append(out.Configs, x.Name)
			}
		}
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return JSONToYAML(raw)
}
