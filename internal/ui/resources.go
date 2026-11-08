package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"swarmlens/internal/api"
)

func newRow(res *resource, id, name string) *row {
	return &row{id: id, name: name, cells: make([]string, len(res.cols)), nums: make([]float64, len(res.cols))}
}

func ageCell(t time.Time, now time.Time) (string, float64) {
	if t.IsZero() {
		return "", 0
	}
	return api.HumanDuration(now.Sub(t)), now.Sub(t).Seconds()
}

func ratioCell(running, desired int) (string, float64) {
	f := 1.0
	if desired > 0 {
		f = float64(running) / float64(desired)
	}
	return fmt.Sprintf("%d/%d", running, desired), f
}

func joinOr(ss []string, sep, empty string) string {
	if len(ss) == 0 {
		return empty
	}
	return strings.Join(ss, sep)
}

func labelCount(m map[string]string) string {
	n := 0
	for k := range m {
		if k != api.StackLabel {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

// ---- services ----

const (
	svcStack = iota
	svcName
	svcMode
	svcReplicas
	svcImage
	svcPorts
	svcUpdate
	svcAge
	svcID
	svcConstraints
	svcUpdated
)

var serviceRes = &resource{
	key: "services", singular: "service", aliases: []string{"svc", "srv"},
	kind: api.KindService, scoped: true, defSort: svcName, defAsc: true,
	cols: []column{
		{name: "STACK", scoped: true, hotkey: "S", min: 10},
		{name: "NAME", hotkey: "N", min: 22, max: 48},
		{name: "MODE", hotkey: "M", min: 10, drop: 1},
		{name: "REPLICAS", hotkey: "R", kind: sortNumber, align: alignRight},
		{name: "IMAGE", hotkey: "I", min: 16, max: 44, drop: 1},
		{name: "PORTS", hotkey: "P", min: 14, max: 30, drop: 3},
		{name: "UPDATE", hotkey: "U", min: 8, drop: 2},
		{name: "AGE", hotkey: "A", kind: sortNumber, align: alignRight, volatile: true},
		{name: "ID", wide: true},
		{name: "CONSTRAINTS", wide: true, max: 40},
		{name: "UPDATED", wide: true, kind: sortNumber, align: alignRight, volatile: true},
	},
}

func loadServices(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
	svcs, err := be.Services(ctx)
	if err != nil {
		return nil, err
	}
	now := q.clock()
	rows := make([]*row, 0, len(svcs))
	for _, s := range svcs {
		r := newRow(serviceRes, s.ID, s.Name)
		r.stack, r.obj = s.Stack, s
		r.cells[svcStack] = s.Stack
		r.cells[svcName] = s.Name
		r.cells[svcMode] = s.Mode
		r.cells[svcReplicas], r.nums[svcReplicas] = ratioCell(s.Running, s.Desired)
		if s.Mode == "replicated-job" {
			r.cells[svcReplicas] += fmt.Sprintf(" (%d/%d)", s.Completed, s.JobTotal)
		}
		r.cells[svcImage] = s.Image
		r.cells[svcPorts] = strings.Join(s.Ports, ", ")
		if s.UpdateState != "completed" {
			r.cells[svcUpdate] = strings.ReplaceAll(s.UpdateState, "_", " ")
		}
		r.cells[svcAge], r.nums[svcAge] = ageCell(s.Created, now)
		r.cells[svcID] = api.ShortID(s.ID)
		r.cells[svcConstraints] = strings.Join(s.Constraints, ", ")
		r.cells[svcUpdated], r.nums[svcUpdated] = ageCell(s.Updated, now)
		r.role = serviceRole(s)
		r.fault = r.role == roleErr
		rows = append(rows, r)
	}
	return rows, nil
}

func serviceRole(s api.Service) role {
	updating := s.UpdateState == "updating" || s.UpdateState == "rollback_started"
	switch {
	case s.UpdateState == "paused" || s.UpdateState == "rollback_paused":
		return roleErr
	case s.Degraded() && updating:
		return rolePending
	case s.Degraded():
		return roleErr
	case updating:
		return rolePending
	case s.Mode == "replicated-job" && s.JobTotal > 0 && s.Completed >= s.JobTotal:
		return roleCompleted
	case s.Replicated() && s.Desired == 0:
		return roleCompleted
	}
	return roleStd
}

func serviceActions(v *tableView) []action {
	svc := func() (api.Service, bool) {
		if r := v.selected(); r != nil {
			s, ok := r.obj.(api.Service)
			return s, ok
		}
		return api.Service{}, false
	}
	return []action{
		act("enter", "Tasks", true, func() tea.Cmd {
			if s, ok := svc(); ok {
				return v.a.pushTasks(loadQuery{serviceID: s.ID}, s.Name)
			}
			return nil
		}),
		describeAct(v, api.KindService),
		yamlAct(v, api.KindService),
		mut("e", "Edit", func() tea.Cmd {
			if s, ok := svc(); ok {
				return v.a.editService(s)
			}
			return nil
		}),
		act("l", "Logs", true, func() tea.Cmd {
			if s, ok := svc(); ok {
				return v.a.pushLogs(api.LogTarget{Kind: api.KindService, ID: s.ID, Name: s.Name}, s.Stack)
			}
			return nil
		}),
		mut("s", "Scale", func() tea.Cmd {
			s, ok := svc()
			switch {
			case !ok:
				return nil
			case !s.Replicated():
				v.a.flash(flashErr, fmt.Sprintf("Service %s runs in %s mode and cannot be scaled", s.Name, s.Mode))
				return nil
			}
			return v.a.scaleService(s)
		}),
		mut("r", "Restart", func() tea.Cmd { return v.a.bulk(v, "Restart", "Restarted", restartOne) }),
		mut("b", "Rollback", func() tea.Cmd {
			s, ok := svc()
			switch {
			case !ok:
				return nil
			case !s.HasPrevSpec:
				v.a.flash(flashWarn, fmt.Sprintf("Service %s has no previous spec to roll back to", s.Name))
				return nil
			}
			return v.a.confirm("Rollback", fmt.Sprintf("Roll back service %q to its previous spec?", s.Name), func() tea.Cmd {
				return v.a.run("Rolled back "+s.Name, func(ctx context.Context) error { return v.a.be.RollbackService(ctx, s.ID) })
			})
		}),
		deleteAct(v, api.KindService, "service"),
	}
}

func restartOne(ctx context.Context, be api.Backend, r *row) error {
	return be.RestartService(ctx, r.id)
}

// ---- tasks ----

const (
	tkStack = iota
	tkName
	tkService
	tkNode
	tkDesired
	tkState
	tkImage
	tkError
	tkAge
	tkID
	tkContainer
	tkSlot
	tkUpdated
)

var taskRes = &resource{
	key: "tasks", singular: "task", aliases: []string{"ps", "tk"},
	kind: api.KindTask, scoped: true, defSort: tkName, defAsc: true,
	cols: []column{
		{name: "STACK", scoped: true, min: 10},
		{name: "NAME", hotkey: "N", min: 22, max: 48},
		{name: "SERVICE", hotkey: "V", min: 16, max: 40, drop: 2, skip: func(q *loadQuery) bool { return q.serviceID != "" }},
		{name: "NODE", min: 10, max: 24},
		{name: "DESIRED", hotkey: "D", min: 8, drop: 1},
		{name: "STATE", hotkey: "S", min: 9},
		{name: "IMAGE", hotkey: "I", min: 16, max: 40, drop: 3},
		{name: "ERROR", hotkey: "E", min: 20, max: 44, drop: 4},
		{name: "AGE", hotkey: "A", kind: sortNumber, align: alignRight, volatile: true},
		{name: "ID", wide: true},
		{name: "CONTAINER", wide: true},
		{name: "SLOT", wide: true, kind: sortNumber, align: alignRight},
		{name: "UPDATED", wide: true, kind: sortNumber, align: alignRight, volatile: true},
	},
}

func loadTasks(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
	tasks, err := be.Tasks(ctx, api.TaskQuery{ServiceID: q.serviceID, NodeID: q.nodeID})
	if err != nil {
		return nil, err
	}
	now := q.clock()
	rows := make([]*row, 0, len(tasks))
	for _, t := range tasks {
		r := newRow(taskRes, t.ID, t.Name)
		r.stack, r.obj = t.Stack, t
		r.cells[tkStack] = t.Stack
		r.cells[tkName] = t.Name
		r.cells[tkService] = t.ServiceName
		r.cells[tkNode] = t.Node
		r.cells[tkDesired] = t.Desired
		r.cells[tkState] = t.State
		r.cells[tkImage] = t.Image
		r.cells[tkError] = t.Error
		r.cells[tkAge], r.nums[tkAge] = ageCell(t.Created, now)
		r.cells[tkID] = api.ShortID(t.ID)
		r.cells[tkContainer] = api.ShortID(t.ContainerID)
		r.cells[tkSlot] = fmt.Sprint(t.Slot)
		r.nums[tkSlot] = float64(t.Slot)
		r.cells[tkUpdated], r.nums[tkUpdated] = ageCell(t.Updated, now)
		r.role = taskRole(t)
		r.fault = t.State == "failed" || t.State == "rejected" || t.State == "orphaned"
		rows = append(rows, r)
	}
	return rows, nil
}

// taskRole colours a task the way k9s colours pods: orange while it is
// coming up, red when it failed, gray once it is history.
func taskRole(t api.Task) role {
	current := t.Desired == "running" || t.Desired == "ready"
	switch t.State {
	case "new", "pending", "assigned", "accepted", "preparing", "ready", "starting":
		if !current {
			return roleCompleted
		}
		return rolePending
	case "running":
		if !current {
			return roleKill // still running but already told to stop
		}
		return roleStd
	case "failed", "rejected", "orphaned":
		if current {
			return roleErr
		}
		return roleCompleted
	case "remove":
		return roleKill
	}
	return roleCompleted // complete, shutdown
}

func taskActions(v *tableView) []action {
	task := func() (api.Task, bool) {
		if r := v.selected(); r != nil {
			t, ok := r.obj.(api.Task)
			return t, ok
		}
		return api.Task{}, false
	}
	logs := func() tea.Cmd {
		if t, ok := task(); ok {
			return v.a.pushLogs(api.LogTarget{Kind: api.KindTask, ID: t.ID, Name: t.Name}, t.Stack)
		}
		return nil
	}
	return []action{
		act("enter", "Logs", true, logs),
		act("l", "Logs", false, logs),
		describeAct(v, api.KindTask),
		yamlAct(v, api.KindTask),
		act("s", "Shell", true, func() tea.Cmd {
			if t, ok := task(); ok {
				return v.a.shell(t)
			}
			return nil
		}),
		act("J", "Jump Owner", true, func() tea.Cmd {
			if t, ok := task(); ok {
				return v.a.jumpToService(t)
			}
			return nil
		}),
	}
}

// ---- nodes ----

const (
	ndName = iota
	ndStatus
	ndAvail
	ndRole
	ndManager
	ndVersion
	ndTasks
	ndCPUR
	ndMemR
	ndAge
	ndArch
	ndOS
	ndCPU
	ndMem
	ndAddr
	ndLabels
)

var nodeRes = &resource{
	key: "nodes", singular: "node", aliases: []string{"no", "nd"},
	kind: api.KindNode, defSort: ndName, defAsc: true,
	cols: []column{
		{name: "NAME", hotkey: "N", min: 10, max: 36},
		{name: "STATUS", hotkey: "S"},
		{name: "AVAIL"},
		{name: "ROLE", hotkey: "R"},
		{name: "MANAGER", hotkey: "M", min: 9, drop: 2},
		{name: "VERSION", hotkey: "V", drop: 1},
		{name: "TASKS", hotkey: "T", kind: sortNumber, align: alignRight},
		{name: "%CPU/R", hotkey: "C", kind: sortNumber, align: alignRight, drop: 3},
		{name: "%MEM/R", kind: sortNumber, align: alignRight, drop: 4},
		{name: "AGE", hotkey: "A", kind: sortNumber, align: alignRight, volatile: true},
		{name: "ARCH", wide: true},
		{name: "OS", wide: true},
		{name: "CPU", wide: true, kind: sortNumber, align: alignRight},
		{name: "MEM", wide: true, kind: sortNumber, align: alignRight},
		{name: "ADDR", wide: true},
		{name: "LABELS", wide: true, max: 40},
	},
}

func pct(used, total float64) (string, float64) {
	if total <= 0 {
		return "n/a", 0
	}
	p := used / total * 100
	return fmt.Sprintf("%.0f%%", p), p
}

func loadNodes(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
	nodes, err := be.Nodes(ctx)
	if err != nil {
		return nil, err
	}
	now := q.clock()
	rows := make([]*row, 0, len(nodes))
	for _, n := range nodes {
		r := newRow(nodeRes, n.ID, n.Hostname)
		r.obj = n
		r.cells[ndName] = n.Hostname
		r.cells[ndStatus] = n.Status
		r.cells[ndAvail] = n.Availability
		r.cells[ndRole] = n.Role
		r.cells[ndManager] = n.Manager
		r.cells[ndVersion] = n.Engine
		r.cells[ndTasks], r.nums[ndTasks] = fmt.Sprint(n.Tasks), float64(n.Tasks)
		r.cells[ndCPUR], r.nums[ndCPUR] = pct(n.CPUReserved, n.CPUs)
		r.cells[ndMemR], r.nums[ndMemR] = pct(float64(n.MemReserved), float64(n.Memory))
		r.cells[ndAge], r.nums[ndAge] = ageCell(n.Created, now)
		r.cells[ndArch] = n.Arch
		r.cells[ndOS] = n.OS
		r.cells[ndCPU], r.nums[ndCPU] = fmt.Sprintf("%.0f", n.CPUs), n.CPUs
		r.cells[ndMem], r.nums[ndMem] = api.HumanBytes(n.Memory), float64(n.Memory)
		r.cells[ndAddr] = n.Addr
		r.cells[ndLabels] = labelList(n.Labels)
		r.role = nodeRole(n)
		r.fault = r.role == roleErr
		rows = append(rows, r)
	}
	return rows, nil
}

func labelList(m map[string]string) string {
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func nodeRole(n api.Node) role {
	switch {
	case n.Status != "ready", n.Manager == "unreachable":
		return roleErr
	case n.Availability == "drain", n.Availability == "pause":
		return rolePending
	}
	return roleStd
}

func nodeActions(v *tableView) []action {
	node := func() (api.Node, bool) {
		if r := v.selected(); r != nil {
			n, ok := r.obj.(api.Node)
			return n, ok
		}
		return api.Node{}, false
	}
	avail := func(a api.NodeAvailability, verb, past string) func() tea.Cmd {
		return func() tea.Cmd {
			return v.a.bulk(v, verb, past, func(ctx context.Context, be api.Backend, r *row) error {
				return be.SetNodeAvailability(ctx, r.id, a)
			})
		}
	}
	role := func(rl api.NodeRole) func() tea.Cmd {
		return func() tea.Cmd {
			n, ok := node()
			if !ok {
				return nil
			}
			if string(rl) == n.Role {
				v.a.flash(flashWarn, fmt.Sprintf("Node %s is already a %s", n.Hostname, n.Role))
				return nil
			}
			verb, past := "Promote", "Promoted"
			if rl == api.RoleWorker {
				verb, past = "Demote", "Demoted"
			}
			return v.a.confirm(verb, fmt.Sprintf("%s node %q to %s?", verb, n.Hostname, rl), func() tea.Cmd {
				return v.a.run(fmt.Sprintf("%s %s", past, n.Hostname), func(ctx context.Context) error {
					return v.a.be.SetNodeRole(ctx, n.ID, rl)
				})
			})
		}
	}
	return []action{
		act("enter", "Tasks", true, func() tea.Cmd {
			if n, ok := node(); ok {
				return v.a.pushTasks(loadQuery{nodeID: n.ID}, "node:"+n.Hostname)
			}
			return nil
		}),
		describeAct(v, api.KindNode),
		yamlAct(v, api.KindNode),
		mut("c", "Cordon", avail(api.AvailPause, "Cordon", "Cordoned")),
		mut("u", "Uncordon", avail(api.AvailActive, "Uncordon", "Uncordoned")),
		mut("r", "Drain", func() tea.Cmd {
			n, ok := node()
			if !ok {
				return nil
			}
			msg := fmt.Sprintf("Drain node %q? Its tasks are rescheduled on other nodes.", n.Hostname)
			if len(v.marks) > 0 {
				msg = fmt.Sprintf("Drain %d nodes? Their tasks are rescheduled on other nodes.", len(v.marks))
			}
			return v.a.confirm("Drain", msg, avail(api.AvailDrain, "Drain", "Drained"))
		}),
		mut("p", "Promote", role(api.RoleManager)),
		mut("D", "Demote", role(api.RoleWorker)),
		deleteAct(v, api.KindNode, "node"),
	}
}

// ---- stacks ----

const (
	stName = iota
	stServices
	stTasks
	stNetworks
	stVolumes
	stSecrets
	stConfigs
	stAge
)

var stackRes = &resource{
	key: "stacks", singular: "stack", aliases: []string{"st"},
	kind: api.KindStack, defSort: stName, defAsc: true,
	cols: []column{
		{name: "NAME", hotkey: "N", min: 12},
		{name: "SERVICES", hotkey: "S", kind: sortNumber, align: alignRight},
		{name: "TASKS", hotkey: "T", kind: sortNumber, align: alignRight},
		{name: "NETWORKS", kind: sortNumber, align: alignRight},
		{name: "VOLUMES", kind: sortNumber, align: alignRight},
		{name: "SECRETS", kind: sortNumber, align: alignRight},
		{name: "CONFIGS", kind: sortNumber, align: alignRight},
		{name: "AGE", hotkey: "A", kind: sortNumber, align: alignRight, volatile: true},
	},
}

func loadStacks(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
	stacks, err := be.Stacks(ctx)
	if err != nil {
		return nil, err
	}
	now := q.clock()
	rows := make([]*row, 0, len(stacks))
	for _, s := range stacks {
		r := newRow(stackRes, s.Name, s.Name)
		r.obj = s
		r.cells[stName] = s.Name
		r.cells[stServices], r.nums[stServices] = fmt.Sprint(s.Services), float64(s.Services)
		r.cells[stTasks], r.nums[stTasks] = ratioCell(s.Running, s.Desired)
		r.cells[stNetworks], r.nums[stNetworks] = fmt.Sprint(s.Networks), float64(s.Networks)
		r.cells[stVolumes], r.nums[stVolumes] = fmt.Sprint(s.Volumes), float64(s.Volumes)
		r.cells[stSecrets], r.nums[stSecrets] = fmt.Sprint(s.Secrets), float64(s.Secrets)
		r.cells[stConfigs], r.nums[stConfigs] = fmt.Sprint(s.Configs), float64(s.Configs)
		r.cells[stAge], r.nums[stAge] = ageCell(s.Created, now)
		if s.Degraded > 0 {
			r.role, r.fault = roleErr, true
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func stackActions(v *tableView) []action {
	return []action{
		act("enter", "Services", true, func() tea.Cmd {
			if r := v.selected(); r != nil {
				return v.a.openStack(r.id)
			}
			return nil
		}),
		describeAct(v, api.KindStack),
		yamlAct(v, api.KindStack),
		deleteAct(v, api.KindStack, "stack"),
	}
}

// ---- networks, volumes, secrets, configs ----

const (
	nwStack = iota
	nwName
	nwDriver
	nwScope
	nwInternal
	nwAttachable
	nwSubnet
	nwAge
	nwID
	nwIPv6
	nwIngress
	nwLabels
)

var networkRes = &resource{
	key: "networks", singular: "network", aliases: []string{"net", "nw"},
	kind: api.KindNetwork, scoped: true, defSort: nwName, defAsc: true,
	cols: []column{
		{name: "STACK", scoped: true, hotkey: "S"},
		{name: "NAME", hotkey: "N", min: 12, max: 44},
		{name: "DRIVER", hotkey: "D"},
		{name: "SCOPE", drop: 1},
		{name: "INTERNAL", drop: 2},
		{name: "ATTACHABLE", drop: 3},
		{name: "SUBNET", min: 14, max: 36, drop: 4},
		{name: "AGE", hotkey: "A", kind: sortNumber, align: alignRight, volatile: true},
		{name: "ID", wide: true},
		{name: "IPV6", wide: true},
		{name: "INGRESS", wide: true},
		{name: "LABELS", wide: true, align: alignRight},
	},
}

func yesNo(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

const (
	vlStack = iota
	vlName
	vlDriver
	vlScope
	vlAge
	vlMount
	vlLabels
)

var volumeRes = &resource{
	key: "volumes", singular: "volume", aliases: []string{"vol", "vo"},
	kind: api.KindVolume, scoped: true, defSort: vlName, defAsc: true,
	cols: []column{
		{name: "STACK", scoped: true, hotkey: "S"},
		{name: "NAME", hotkey: "N", min: 12, max: 56},
		{name: "DRIVER", hotkey: "D"},
		{name: "SCOPE"},
		{name: "AGE", hotkey: "A", kind: sortNumber, align: alignRight, volatile: true},
		{name: "MOUNTPOINT", wide: true, max: 60},
		{name: "LABELS", wide: true, align: alignRight},
	},
}

const (
	scStack = iota
	scName
	scAge
	scUpdated
	scID
	scDriver
	scLabels
)

var secretRes = &resource{
	key: "secrets", singular: "secret", aliases: []string{"sec"},
	kind: api.KindSecret, scoped: true, defSort: scName, defAsc: true,
	cols: []column{
		{name: "STACK", scoped: true, hotkey: "S"},
		{name: "NAME", hotkey: "N", min: 12, max: 56},
		{name: "AGE", hotkey: "A", kind: sortNumber, align: alignRight, volatile: true},
		{name: "UPDATED", hotkey: "U", kind: sortNumber, align: alignRight, volatile: true},
		{name: "ID", wide: true},
		{name: "DRIVER", wide: true},
		{name: "LABELS", wide: true, align: alignRight},
	},
}

var configRes = &resource{
	key: "configs", singular: "config", aliases: []string{"cfg", "conf"},
	kind: api.KindConfig, scoped: true, defSort: scName, defAsc: true,
	cols: []column{
		{name: "STACK", scoped: true, hotkey: "S"},
		{name: "NAME", hotkey: "N", min: 12, max: 56},
		{name: "AGE", hotkey: "A", kind: sortNumber, align: alignRight, volatile: true},
		{name: "UPDATED", hotkey: "U", kind: sortNumber, align: alignRight, volatile: true},
		{name: "ID", wide: true},
		{name: "DRIVER", wide: true},
		{name: "LABELS", wide: true, align: alignRight},
	},
}

// ---- events ----

const (
	evSeen = iota
	evType
	evAction
	evName
	evStack
	evMessage
)

var eventRes = &resource{
	key: "events", singular: "event", aliases: []string{"ev"},
	kind: api.KindEvent, scoped: true, defSort: evSeen, defAsc: true,
	cols: []column{
		{name: "LAST SEEN", hotkey: "L", kind: sortNumber, align: alignRight, volatile: true},
		{name: "TYPE", hotkey: "T"},
		{name: "ACTION", hotkey: "A"},
		{name: "NAME", hotkey: "N", min: 12, max: 44},
		{name: "STACK", scoped: true, hotkey: "S"},
		{name: "MESSAGE", max: 80},
	},
}

// eventMessage summarises the attributes of an event, leaving out what the
// other columns already say.
func eventMessage(e api.Event) string {
	if m := e.Attrs["message"]; m != "" {
		return m
	}
	var kv []string
	for k, v := range e.Attrs {
		if k == "name" || strings.HasPrefix(k, "com.docker.") {
			continue
		}
		kv = append(kv, k+"="+v)
	}
	sort.Strings(kv)
	return strings.Join(kv, " ")
}

// ---- contexts ----

var contextRes = &resource{
	key: "contexts", singular: "context", aliases: []string{"ctx"},
	kind: "context", defSort: 0, defAsc: true,
	cols: []column{
		{name: "NAME", hotkey: "N", min: 12},
		{name: "DESCRIPTION", max: 50},
		{name: "ENDPOINT", max: 60},
	},
}

// resources is the registry behind the ":" prompt.
var resources = []*resource{
	serviceRes, taskRes, nodeRes, stackRes, networkRes, volumeRes, secretRes, configRes, eventRes, contextRes,
}

// load and actions are wired here rather than in the literals above: the
// actions drill into other resources, which would make the package-level
// variables depend on each other.
func init() {
	serviceRes.load = loadServices
	serviceRes.actions = serviceActions
	taskRes.load = loadTasks
	taskRes.actions = taskActions
	nodeRes.load = loadNodes
	nodeRes.actions = nodeActions
	stackRes.load = loadStacks
	stackRes.actions = stackActions
	networkRes.load = func(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
		nets, err := be.Networks(ctx)
		if err != nil {
			return nil, err
		}
		now := q.clock()
		rows := make([]*row, 0, len(nets))
		for _, n := range nets {
			r := newRow(networkRes, n.ID, n.Name)
			r.stack, r.obj = n.Stack, n
			r.cells[nwStack] = n.Stack
			r.cells[nwName] = n.Name
			r.cells[nwDriver] = n.Driver
			r.cells[nwScope] = n.Scope
			r.cells[nwInternal] = yesNo(n.Internal)
			r.cells[nwAttachable] = yesNo(n.Attachable)
			r.cells[nwSubnet] = strings.Join(n.Subnets, ", ")
			r.cells[nwAge], r.nums[nwAge] = ageCell(n.Created, now)
			r.cells[nwID] = api.ShortID(n.ID)
			r.cells[nwIPv6] = yesNo(n.IPv6)
			r.cells[nwIngress] = yesNo(n.Ingress)
			r.cells[nwLabels] = labelCount(n.Labels)
			rows = append(rows, r)
		}
		return rows, nil
	}
	networkRes.actions = func(v *tableView) []action {
		return []action{describeAct(v, api.KindNetwork), yamlAct(v, api.KindNetwork), deleteAct(v, api.KindNetwork, "network")}
	}
	volumeRes.load = func(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
		vols, err := be.Volumes(ctx)
		if err != nil {
			return nil, err
		}
		now := q.clock()
		rows := make([]*row, 0, len(vols))
		for _, vo := range vols {
			r := newRow(volumeRes, vo.Name, vo.Name)
			r.stack, r.obj = vo.Stack, vo
			r.cells[vlStack] = vo.Stack
			r.cells[vlName] = vo.Name
			r.cells[vlDriver] = vo.Driver
			r.cells[vlScope] = vo.Scope
			r.cells[vlAge], r.nums[vlAge] = ageCell(vo.Created, now)
			r.cells[vlMount] = vo.Mountpoint
			r.cells[vlLabels] = labelCount(vo.Labels)
			rows = append(rows, r)
		}
		return rows, nil
	}
	volumeRes.actions = func(v *tableView) []action {
		return []action{describeAct(v, api.KindVolume), yamlAct(v, api.KindVolume), deleteAct(v, api.KindVolume, "volume")}
	}
	secretRes.load = func(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
		secs, err := be.Secrets(ctx)
		if err != nil {
			return nil, err
		}
		now := q.clock()
		rows := make([]*row, 0, len(secs))
		for _, s := range secs {
			r := newRow(secretRes, s.ID, s.Name)
			r.stack, r.obj = s.Stack, s
			r.cells[scStack] = s.Stack
			r.cells[scName] = s.Name
			r.cells[scAge], r.nums[scAge] = ageCell(s.Created, now)
			r.cells[scUpdated], r.nums[scUpdated] = ageCell(s.Updated, now)
			r.cells[scID] = api.ShortID(s.ID)
			r.cells[scDriver] = s.Driver
			r.cells[scLabels] = labelCount(s.Labels)
			rows = append(rows, r)
		}
		return rows, nil
	}
	secretRes.actions = func(v *tableView) []action {
		return []action{describeAct(v, api.KindSecret), yamlAct(v, api.KindSecret), deleteAct(v, api.KindSecret, "secret")}
	}
	configRes.load = func(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
		cfgs, err := be.Configs(ctx)
		if err != nil {
			return nil, err
		}
		now := q.clock()
		rows := make([]*row, 0, len(cfgs))
		for _, c := range cfgs {
			r := newRow(configRes, c.ID, c.Name)
			r.stack, r.obj = c.Stack, c
			r.cells[scStack] = c.Stack
			r.cells[scName] = c.Name
			r.cells[scAge], r.nums[scAge] = ageCell(c.Created, now)
			r.cells[scUpdated], r.nums[scUpdated] = ageCell(c.Updated, now)
			r.cells[scID] = api.ShortID(c.ID)
			r.cells[scLabels] = labelCount(c.Labels)
			rows = append(rows, r)
		}
		return rows, nil
	}
	configRes.actions = func(v *tableView) []action {
		return []action{describeAct(v, api.KindConfig), yamlAct(v, api.KindConfig), deleteAct(v, api.KindConfig, "config")}
	}
	eventRes.load = func(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
		now := q.clock()
		rows := make([]*row, 0, len(q.events))
		for i, e := range q.events {
			r := newRow(eventRes, fmt.Sprintf("%d/%d", e.Time.UnixNano(), i), e.Name)
			r.stack, r.obj = e.Stack, e
			r.cells[evSeen], r.nums[evSeen] = ageCell(e.Time, now)
			r.cells[evType] = e.Type
			r.cells[evAction] = e.Action
			r.cells[evName] = e.Name
			r.cells[evStack] = e.Stack
			r.cells[evMessage] = eventMessage(e)
			switch {
			case e.Type == "error", e.Action == "die", e.Action == "oom", e.Action == "kill":
				r.role, r.fault = roleErr, true
			case e.Action == "remove", e.Action == "destroy":
				r.role = roleKill
			}
			rows = append(rows, r)
		}
		return rows, nil
	}
	eventRes.actions = func(v *tableView) []action { return nil }
	contextRes.load = func(ctx context.Context, be api.Backend, q loadQuery) ([]*row, error) {
		sw, ok := be.(api.Switcher)
		if !ok {
			return nil, fmt.Errorf("this backend has no docker contexts")
		}
		list, err := sw.Contexts()
		if err != nil {
			return nil, err
		}
		rows := make([]*row, 0, len(list))
		for _, c := range list {
			r := newRow(contextRes, c.Name, c.Name)
			r.obj = c
			r.cells[0] = c.Name
			if c.Current {
				r.cells[0] += "(*)"
				r.role = roleHighlight
			}
			r.cells[1] = c.Description
			r.cells[2] = c.Endpoint
			rows = append(rows, r)
		}
		return rows, nil
	}
	contextRes.actions = func(v *tableView) []action {
		return []action{
			act("enter", "Use", true, func() tea.Cmd {
				if r := v.selected(); r != nil {
					return v.a.switchContext(r.id)
				}
				return nil
			}),
		}
	}
}
