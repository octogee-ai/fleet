package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/octogee-ai/octogee-fleet/internal/admin"
	"github.com/octogee-ai/octogee-fleet/internal/terminal"
)

// change is one mutation: the request, and how to say what it did.
type change struct {
	request admin.Request
	done    string
	notes   []string
	next    []string
}

func post(path string, body map[string]any) admin.Request {
	return admin.Request{Method: http.MethodPost, Path: path, Body: body}
}

// command is a full octogee-fleet command line for this environment.
func (i *invocation) command(arguments string) string {
	return fmt.Sprintf("octogee-fleet --env=%s %s", i.name, arguments)
}

func (i *invocation) apply(c change) error {
	request, data, applied, err := i.send(c)
	if err != nil || !applied {
		return err
	}
	if i.json {
		return i.printJSON(changeDocument{Request: describeRequest(request), Response: rawJSON(data)})
	}
	i.printResult(c, data)
	return nil
}

// send prints the request, then dry-runs it or, with --apply, sends it. It
// returns the request as sent.
func (i *invocation) send(c change) (admin.Request, []byte, bool, error) {
	request := c.request
	request.IdempotencyKey = i.value("idempotency-key")
	if !i.json {
		i.printRequest(request)
	}
	for _, note := range c.notes {
		i.caution(note)
	}
	if !i.has("apply") {
		return request, nil, false, i.dryRun(request)
	}
	data, err := i.admin.Send(i.ctx, request)
	if err != nil {
		return request, nil, false, err
	}
	return request, data, true, nil
}

// caution is a warning a person must see; with --format=json it goes to stderr.
func (i *invocation) caution(text string) {
	if i.json {
		i.notice(text)
		return
	}
	i.warn(text)
}

// unchanged reports a change that is already in place.
func (i *invocation) unchanged(reason string) error {
	if i.json {
		return i.printJSON(map[string]any{"unchanged": true, "reason": reason})
	}
	i.ok(reason + "; nothing to do")
	return nil
}

// changeDocument is a mutation as --format=json prints it.
type changeDocument struct {
	DryRun   bool              `json:"dryRun"`
	Checked  *bool             `json:"checked,omitempty"`
	Request  requestDocument   `json:"request"`
	Plans    []json.RawMessage `json:"plans,omitempty"`
	Response json.RawMessage   `json:"response,omitempty"`
}

type requestDocument struct {
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Query          map[string]string `json:"query,omitempty"`
	Body           any               `json:"body,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey,omitempty"`
}

func describeRequest(request admin.Request) requestDocument {
	document := requestDocument{Method: request.Method, Path: request.Path, IdempotencyKey: request.IdempotencyKey}
	if len(request.Query) > 0 {
		document.Query = map[string]string{}
		for name := range request.Query {
			document.Query[name] = request.Query.Get(name)
		}
	}
	if request.Body != nil {
		document.Body = request.Body
	}
	return document
}

func dryRunDocument(request admin.Request, checked bool, plans []json.RawMessage) changeDocument {
	return changeDocument{DryRun: true, Checked: &checked, Request: describeRequest(request), Plans: plans}
}

// rawJSON is an Admin API response to embed in JSON output.
func rawJSON(data []byte) json.RawMessage {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if !json.Valid(data) {
		quoted, _ := json.Marshal(string(data))
		return quoted
	}
	return data
}

func (i *invocation) printRequest(request admin.Request) {
	line := request.Method + " " + request.Target()
	if !i.style.glyphs {
		i.line(line)
		if request.Body != nil {
			data, _ := json.MarshalIndent(request.Body, "", "  ")
			i.line(string(data))
		}
		return
	}
	i.line(i.style.bold(request.Method) + " " + request.Target())
	if request.Body != nil {
		data, _ := json.Marshal(request.Body)
		i.line("  " + i.style.dim(string(data)))
	}
}

func (i *invocation) banner(text string) {
	if i.style.glyphs {
		i.line(i.style.yellow("●") + " " + i.style.yellow(text))
		return
	}
	i.line(text)
}

func (i *invocation) dryRun(request admin.Request) error {
	if i.has("offline") {
		return i.unchecked(request, "dry run, nothing sent; add --apply to do it")
	}
	if !i.admin.Supports(admin.DryRun) {
		return i.unchecked(request, "dry run, nothing sent: this Admin API does not answer dry runs, so it has not checked this request; add --apply to do it")
	}
	data, err := i.admin.Send(i.ctx, request.AsDryRun())
	if err != nil {
		return dryRunRefusal(err)
	}
	var answer struct {
		DryRun bool              `json:"dryRun"`
		Plans  []json.RawMessage `json:"plans"`
	}
	if err := json.Unmarshal(data, &answer); err != nil || !answer.DryRun {
		return fmt.Errorf("the Admin API did not answer with a plan, so the change may have been applied; it returned: %s", terminal.Escape(strings.TrimSpace(string(data))))
	}
	if len(answer.Plans) == 0 {
		return fmt.Errorf("the Admin API answered the dry run without saying what it would do; it returned: %s", terminal.Escape(strings.TrimSpace(string(data))))
	}
	if i.json {
		return i.printJSON(dryRunDocument(request, true, answer.Plans))
	}
	plans := make([]dryRunPlan, len(answer.Plans))
	for index, raw := range answer.Plans {
		if err := decodeForDisplay(raw, &plans[index]); err != nil {
			return fmt.Errorf("decode the dry run's plan: %w", err)
		}
	}
	i.printPlans(plans)
	i.banner("dry run, nothing changed; add --apply to do it")
	i.detail(i.style.dim("checks made while the change is applied are not predicted"))
	return nil
}

// unchecked reports a dry run that the Admin API did not check.
func (i *invocation) unchecked(request admin.Request, banner string) error {
	if i.json {
		return i.printJSON(dryRunDocument(request, false, nil))
	}
	i.banner(banner)
	return nil
}

func dryRunRefusal(err error) error {
	var refused *admin.HTTPError
	if !errors.As(err, &refused) || refused.Status == http.StatusUnauthorized || refused.Status == http.StatusForbidden {
		return err
	}
	return fmt.Errorf("dry run: the Admin API would refuse this request (%s): %s\nnothing was changed", refused.Label(), terminal.Escape(refused.Message))
}

type dryRunPlan struct {
	What         string         `json:"what"`
	Target       map[string]any `json:"target"`
	CurrentState map[string]any `json:"preActionStatus"`
	Forced       bool           `json:"forced"`
}

func (i *invocation) printPlans(plans []dryRunPlan) {
	for _, plan := range plans {
		var forced string
		if plan.Forced {
			forced = i.style.red("yes")
		}
		if !i.style.glyphs {
			i.line("dry run: the Admin API would " + plan.What)
			for _, f := range []field{{"target", fieldList(plan.Target)}, {"current state", fieldList(plan.CurrentState)}, {"forced", forced}} {
				if f.value != "" {
					i.line("  " + f.label + ": " + f.value)
				}
			}
			continue
		}
		i.line("The Admin API would " + i.style.bold(plan.What))
		i.fields([]field{{"target", fieldList(plan.Target)}, {"current state", fieldList(plan.CurrentState)}, {"forced", forced}})
	}
}

func fieldList(fields map[string]any) string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+fieldText(fields[name]))
	}
	return strings.Join(parts, " ")
}

func fieldText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

type operationRef struct {
	label, id string
}

// operationIDs finds the operations an applied change started: operationId
// first, then any other field that names one, such as erasureOperationId.
func operationIDs(data []byte) []operationRef {
	var answer map[string]any
	if decodeForDisplay(data, &answer) != nil {
		return nil
	}
	var refs []operationRef
	if id, ok := answer["operationId"].(string); ok && id != "" {
		refs = append(refs, operationRef{"operation", id})
	}
	var names []string
	for name := range answer {
		if name != "operationId" && strings.HasSuffix(name, "OperationId") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		if id, ok := answer[name].(string); ok && id != "" {
			refs = append(refs, operationRef{strings.TrimSuffix(name, "OperationId") + " operation", id})
		}
	}
	return refs
}

func (i *invocation) printResult(c change, data []byte) {
	if i.style.glyphs {
		i.ok(c.done)
	} else {
		i.line("applied: " + c.done)
	}
	refs := operationIDs(data)
	for _, ref := range refs {
		i.detail(ref.label + " " + i.style.bold(ref.id))
	}
	if len(refs) == 0 {
		i.detail("the Admin API returned no operation id; the change is recorded in the journal")
	}
	switch {
	case len(c.next) > 0:
		for _, next := range c.next {
			i.next(next)
		}
	case len(refs) == 0:
		i.next(i.command("ops ls"))
	default:
		i.next(i.command("ops inspect " + refs[0].id))
	}
}

func (i *invocation) replaceVM(path, done string) error {
	force, accept := i.has("force"), i.has("accept-boot-data-loss")
	if force != accept {
		return errors.New("replacing a VM with boot-disk loss requires both --force and --accept-boot-data-loss")
	}
	if !force {
		return i.apply(change{request: post(path, map[string]any{}), done: done})
	}
	return i.apply(change{
		request: post(path, map[string]any{"force": true, "acceptBootDataLoss": true}),
		done:    done,
		notes:   []string{"BREAK GLASS: data on the boot disk, including unpublished work, may be lost; the verified home disk is kept"},
	})
}
