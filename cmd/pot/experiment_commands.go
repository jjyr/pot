package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/internal/transport"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"io"
	"net/url"
	"strconv"
	"text/tabwriter"
)

type experimentStreams func(context.Context, string, string, func(localui.Event) error) error

type experimentAPI func(context.Context, string, string, string, any) (json.RawMessage, error)

func experimentCommand(ctx context.Context, args []string, in io.Reader, out, diagnostics io.Writer) error {
	o, err := parseExperimentOptions(args, in)
	if err != nil {
		return err
	}
	endpoint, err := transport.ReadEndpoint(o.Root)
	if err != nil {
		return err
	}
	current := func() (transport.Endpoint, error) {
		next, err := transport.ReadEndpoint(o.Root)
		if err != nil {
			return next, err
		}
		if next.RuntimeID != endpoint.RuntimeID {
			return next, protocol.APIError{Code: "identity_changed", Message: "Runtime identity changed"}
		}
		return next, nil
	}
	api := func(ctx context.Context, owner, method, path string, body any) (json.RawMessage, error) {
		next, err := current()
		if err != nil {
			return nil, err
		}
		u, err := url.Parse(path)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("assistant_id", owner)
		u.RawQuery = q.Encode()
		var raw json.RawMessage
		if body != nil {
			raw, err = json.Marshal(body)
			if err != nil {
				return nil, err
			}
		}
		return localui.NewLocalHTTPClient(next.Client(), next.Token, endpoint.RuntimeID).Call(ctx, method, u.String(), raw)
	}
	streams := func(ctx context.Context, owner, request string, receive func(localui.Event) error) error {
		next, err := current()
		if err != nil {
			return err
		}
		q := url.Values{"assistant_id": {owner}, "request_id": {request}}
		paths := []string{"/api/v1/execution-status?" + q.Encode(), "/api/v1/events?assistant_id=" + url.QueryEscape(owner)}
		return localui.NewLocalHTTPClient(next.Client(), next.Token, endpoint.RuntimeID).StreamMany(ctx, paths, func(_ string, event localui.Event) error { return receive(event) })
	}
	return executeExperiment(ctx, o, api, streams, out, diagnostics)
}

func executeExperiment(ctx context.Context, o experimentOptions, api experimentAPI, streams experimentStreams, out, diagnostics io.Writer) error {
	if o.Command == "usage" {
		q := url.Values{"start": {o.From}, "end": {o.To}, "timezone": {o.Timezone}, "task_id": {o.Task}, "turn": {o.Turn}, "details": {strconv.FormatBool(o.Details)}, "offset": {strconv.Itoa(o.Offset)}}
		raw, err := api(ctx, o.Assistant, "GET", "/api/v1/operator/usage?"+q.Encode(), nil)
		if err != nil {
			return err
		}
		if o.JSON {
			return printUserJSON(out, raw)
		}
		var report control.ExperimentUsage
		if err = json.Unmarshal(raw, &report); err != nil {
			return err
		}
		return printExperimentUsage(out, report)
	}
	// Resolve the local directory before submitting, so an unsupported or ambiguous
	// assistant cannot accept work which this command cannot observe.
	raw, err := api(ctx, "", "GET", "/api/v1/assistants", nil)
	if err != nil {
		return err
	}
	var defs []assistant.Definition
	if err = json.Unmarshal(raw, &defs); err != nil {
		return err
	}
	if o.Assistant == "" {
		if len(defs) != 1 {
			return errors.New("ask requires --assistant when there is not exactly one local assistant")
		}
		o.Assistant = defs[0].ID
	}
	found := false
	for _, d := range defs {
		if d.ID == o.Assistant {
			found = true
		}
	}
	if !found {
		return errors.New("ask requires a local assistant")
	}
	raw, err = api(ctx, o.Assistant, "GET", "/api/v1/snapshot", nil)
	if err != nil {
		return err
	}
	var snapshot protocol.UIFrame
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	request := storage.ID()
	fmt.Fprintf(diagnostics, "assistant=%s request=%s\n", o.Assistant, request)
	raw, err = api(ctx, o.Assistant, "POST", "/api/v1/operator/ask", control.AskRequest{Message: protocol.SendMessage{RequestID: request, ConversationID: snapshot.ConversationID, Text: o.Prompt}, Model: o.Model, Effort: o.Effort, Provider: o.Provider, TaskModel: o.TaskModel, TaskEffort: o.TaskEffort, TaskProvider: o.TaskProvider})
	if err != nil {
		return fmt.Errorf("submission not confirmed (request %s); inspect the conversation before resubmitting: %w", request, err)
	}
	var receipt protocol.SendMessageAccepted
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return err
	}
	if o.NoWait {
		status := protocol.AgentExecutionStatus{AssistantID: o.Assistant, RequestID: request, MessageID: receipt.MessageID, State: "accepted"}
		if o.JSON {
			return printUserJSON(out, status)
		}
		_, err = fmt.Fprintf(out, "accepted assistant=%s request=%s message=%s\n", o.Assistant, request, receipt.MessageID)
		return err
	}
	return waitExperiment(ctx, o, request, api, streams, out, diagnostics)
}

func tokenNumber(n int64, known bool) string {
	if !known {
		return fmt.Sprintf("%d (partial/unknown)", n)
	}
	return strconv.FormatInt(n, 10)
}
func optionalTokens(n *int64) string {
	if n == nil {
		return "unknown"
	}
	return strconv.FormatInt(*n, 10)
}
func printExperimentUsage(out io.Writer, r control.ExperimentUsage) error {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	scope := r.Scope
	if scope == "" {
		scope = "all"
	}
	fmt.Fprintf(w, "Scope: %s  Dates: %s..%s (%s)\n", scope, r.Start, r.End, r.Timezone)
	if r.Turn != "" {
		fmt.Fprintf(w, "Turn: %s\n", r.Turn)
	}
	fmt.Fprintln(w, "Actual model usage (cache is a subset of input)")
	if !r.UsageAvailable {
		fmt.Fprintln(w, "Remote execution usage unavailable in this local ledger.")
	}
	if len(r.Models) == 0 {
		fmt.Fprintln(w, "No recorded model requests.")
	}
	fmt.Fprintln(w, "AGENT\tSTAGE\tREQUESTS\tINPUT\tOUTPUT\tCACHE")
	for _, v := range r.Models {
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", v.Agent, v.Stage, v.Requests, tokenNumber(v.Input, v.InputKnown), tokenNumber(v.Output, v.OutputKnown), tokenNumber(v.Cache, v.CacheKnown))
	}
	fmt.Fprintln(w, "Tool content estimates (do not add to actual model usage)")
	fmt.Fprintln(w, "TOOL\tAGENT\tCALLS\tFAILED\tUNFINISHED\tARGUMENT TOKENS\tRESULT TOKENS")
	for _, v := range r.ToolGroups {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%s\t%s\n", v.Name, v.Agent, v.Calls, v.Failed, v.Unfinished, tokenNumber(v.CallEstimatedTokens, v.CallsKnown), tokenNumber(v.ResultEstimatedTokens, v.ResultsKnown))
	}
	if r.Communications == nil {
		fmt.Fprintln(w, "Communication attribution unavailable for a single turn.")
	} else {
		c := r.Communications
		fmt.Fprintf(w, "Communications: %d messages, estimated content tokens: %s\n", c.Count, tokenNumber(c.ContentEstimatedTokens, c.EstimatesKnown))
		for _, m := range c.Items {
			fmt.Fprintf(w, "%s\t%s\t%s:%s -> %s:%s\t%s\t%s\n", m.ID, m.Kind, m.From.Kind, m.From.AgentID, m.To.Kind, m.To.AgentID, m.State, optionalTokens(m.ContentEstimatedTokens))
		}
		if c.NextOffset != nil {
			fmt.Fprintf(w, "More communications: --details --offset %d\n", *c.NextOffset)
		}
	}
	if r.ToolRecords != nil {
		// Page.Items is polymorphic; decode its concrete projection for CLI display.
		data, err := json.Marshal(r.ToolRecords.Items)
		if err != nil {
			return err
		}
		var records []usage.ToolRecord
		if err = json.Unmarshal(data, &records); err != nil {
			return err
		}
		fmt.Fprintln(w, "CALL ID\tSCOPE\tTURN\tTOOL\tARGUMENT TOKENS\tRESULT TOKENS")
		for _, v := range records {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", v.ID, v.Scope, v.Turn, v.Name, optionalTokens(v.CallTokens), optionalTokens(v.ResultTokens))
		}
		if r.ToolRecords.NextOffset != nil {
			fmt.Fprintf(w, "More tool calls: --details --offset %d\n", *r.ToolRecords.NextOffset)
		}
	}
	return w.Flush()
}
