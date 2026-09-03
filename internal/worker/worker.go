// Copyright (c) 2025 JoeGlenn1213
// ActionD Worker - Executes plugin tasks

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/JoeGlenn1213/actiond/internal/artifact"
	"github.com/JoeGlenn1213/actiond/internal/event"
	"github.com/JoeGlenn1213/actiond/internal/job"
	"github.com/JoeGlenn1213/actiond/internal/plugin"
	"github.com/JoeGlenn1213/actiond/internal/pubsub"
	"github.com/JoeGlenn1213/actiond/internal/repopath"
	"github.com/JoeGlenn1213/actiond/internal/store"
)

// Task represents a unit of work for the worker
type Task struct {
	Plugin  plugin.Plugin
	Event   event.Event
	Profile string // Active execution profile (verdict tier provenance)
	Intent  string // task/goal id recorded at dispatch (native contract v1)
}

type queuedTask struct {
	Job  *job.ActionJob
	Task Task
}

// Worker executes plugin tasks from a queue
// First version: single goroutine, serial execution
type Worker struct {
	queue     chan queuedTask
	done      chan struct{}
	artifacts *artifact.Store
	store     store.Store
	repoRoot  string
	pubsub    *pubsub.PubSub

	mu             sync.Mutex
	taskByJob      map[string]Task
	cancels        map[string]context.CancelFunc
	cancelled      map[string]bool
	pluginResolver func(string) plugin.Plugin
	dedupSet       map[string]bool                                       // deduplication: eventID:pluginName -> true
	statusCallback func(repo, commitSHA, plugin, status, summary string) // Callback to report CI status

	reposDir     string // LGH bare repository root; enables isolated checkouts
	checkoutRoot string // root for isolated checkouts (default: ~/.localgithub/checkouts)

	wg       sync.WaitGroup // tracks the execution goroutine so Stop waits for it
	stopOnce sync.Once
}

// SetCheckoutDirs enables isolated checkouts: plugin jobs run against a
// clean checkout of the pushed sha instead of the developer's live working
// tree. Leave reposDir empty to keep the legacy working-tree behaviour
// (used by tests and standalone runs).
func (w *Worker) SetCheckoutDirs(reposDir, checkoutRoot string) {
	w.reposDir = reposDir
	w.checkoutRoot = checkoutRoot
}

// StatusUpdate represents a status update for a commit
type StatusUpdate struct {
	Repo      string
	CommitSHA string
	Plugin    string
	Status    string
	Summary   string
}

// SetStatusCallback sets a callback function to be called when a job completes
func (w *Worker) SetStatusCallback(fn func(repo, commitSHA, plugin, status, summary string)) {
	w.statusCallback = fn
}

// NewWorker creates a new worker with a buffered task queue
func NewWorker(bufferSize int, artifactStore *artifact.Store, jobStore store.Store, repoRoot string) *Worker {
	if bufferSize <= 0 {
		bufferSize = 10
	}
	return &Worker{
		queue:     make(chan queuedTask, bufferSize),
		done:      make(chan struct{}),
		artifacts: artifactStore,
		store:     jobStore,
		repoRoot:  repoRoot,
		pubsub:    pubsub.New(),
		taskByJob: make(map[string]Task),
		cancels:   make(map[string]context.CancelFunc),
		cancelled: make(map[string]bool),
	}
}

// PubSub returns the worker's PubSub instance for external use (e.g., SSE endpoint)
func (w *Worker) PubSub() *pubsub.PubSub {
	return w.pubsub
}

// SetPubSub sets the PubSub instance
func (w *Worker) SetPubSub(ps *pubsub.PubSub) {
	w.pubsub = ps
}

// SetPluginResolver configures how retry reconstructs plugins by name.
func (w *Worker) SetPluginResolver(fn func(string) plugin.Plugin) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pluginResolver = fn
}

// Start begins processing tasks
func (w *Worker) Start() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for {
			select {
			case task := <-w.queue:
				w.execute(task)
			case <-w.done:
				return
			}
		}
	}()
}

// Submit creates a pending job and enqueues it for execution.
// Returns empty string if the task is a duplicate of an already-queued job.
func (w *Worker) Submit(t Task) string {
	return w.submitJob(newJobForTask(t), t)
}

// submitJob dedups, persists and enqueues a pre-built job.
func (w *Worker) submitJob(j *job.ActionJob, t Task) string {
	// Deduplication: skip if same event+plugin job already in queue
	dedupKey := t.Event.ID + ":" + t.Plugin.Name()
	w.mu.Lock()
	if w.dedupSet == nil {
		w.dedupSet = make(map[string]bool)
	}
	if w.dedupSet[dedupKey] {
		w.mu.Unlock()
		return "" // skip duplicate
	}
	w.dedupSet[dedupKey] = true
	w.mu.Unlock()

	if w.store != nil {
		if err := w.store.AddJob(j); err != nil {
			if errors.Is(err, store.ErrDuplicateJob) {
				// Another dispatch (e.g. a second daemon instance) already
				// persisted this event+plugin pair — do NOT enqueue.
				w.mu.Lock()
				delete(w.dedupSet, dedupKey)
				w.mu.Unlock()
				fmt.Printf("   ⏭️  duplicate dispatch skipped (%s, %s)\n", t.Event.ID, t.Plugin.Name())
				return ""
			}
			fmt.Printf("⚠️ Failed to add job to store: %v\n", err)
		}
	}

	w.mu.Lock()
	w.taskByJob[j.ID] = t
	w.mu.Unlock()

	w.queue <- queuedTask{Job: j, Task: t}
	return j.ID
}

// Requeue re-queues an existing persisted job after a restart without
// creating a new id — the row is reset to pending and re-executed
// (ASSURANCE §7 前置债: pending 恢复).
func (w *Worker) Requeue(j *job.ActionJob, t Task) bool {
	j.Status = job.StatusPending
	j.Progress = "requeued after restart"
	j.StartedAt = nil
	if w.store != nil {
		if err := w.store.UpdateJob(j); err != nil {
			fmt.Printf("⚠️ Failed to persist requeue state for %s: %v\n", j.ID, err)
		}
	}

	dedupKey := t.Event.ID + ":" + t.Plugin.Name()
	w.mu.Lock()
	if w.dedupSet == nil {
		w.dedupSet = make(map[string]bool)
	}
	if w.dedupSet[dedupKey] {
		w.mu.Unlock()
		return false // already queued in this instance
	}
	w.dedupSet[dedupKey] = true
	w.taskByJob[j.ID] = t
	w.mu.Unlock()

	w.queue <- queuedTask{Job: j, Task: t}
	return true
}

// Cancel cancels a running or queued job.
func (w *Worker) Cancel(jobID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if cancel, ok := w.cancels[jobID]; ok {
		cancel()
		w.cancelled[jobID] = true
		return true
	}

	if _, ok := w.taskByJob[jobID]; ok {
		w.cancelled[jobID] = true
		return true
	}

	return false
}

// Retry requeues a previously executed task and returns the new job ID.
func (w *Worker) Retry(jobID string) (string, error) {
	task, err := w.taskForRetry(jobID)
	if err != nil {
		return "", err
	}
	j := newJobForTask(task)
	// Mark as retry so the dispatch dedup index (unique event_id+plugin_name
	// for non-retry rows) does not swallow the new job as a duplicate —
	// this was the root cause of actiond_job_retry silently not re-running.
	j.MarkRetry(jobID, "", 0)
	if w.store != nil {
		if orig, err := w.store.GetJob(jobID); err == nil && orig != nil {
			j.MarkRetry(jobID, orig.RunID, orig.RetryCount+1)
		}
	}
	return w.submitJob(j, task), nil
}

func (w *Worker) execute(entry queuedTask) {
	j := entry.Job
	task := entry.Task

	// Clean up dedup entry when execution finishes (regardless of outcome)
	dedupKey := task.Event.ID + ":" + task.Plugin.Name()
	defer func() {
		w.mu.Lock()
		delete(w.dedupSet, dedupKey)
		w.mu.Unlock()

		// Notify status callback if configured
		if w.statusCallback != nil && j.CommitSHA != "" {
			status := "success"
			switch j.Status {
			case "failed":
				status = "failure"
			case "cancelled":
				status = "cancelled"
			case "error":
				status = "error"
			}
			w.statusCallback(task.Event.Repo, j.CommitSHA, task.Plugin.Name(), status, j.Progress)
		}
	}()

	if w.isCancelled(j.ID) {
		w.finishCancelledJob(j, "Cancelled before execution")
		return
	}

	start := time.Now()

	// Publish start message
	w.pubsub.PublishLine(j.ID, fmt.Sprintf("🚀 Starting plugin: %s (RunID: %s)", task.Plugin.Name(), j.RunID), false)

	// Create artifact writer for this execution
	var artifactWriter *artifact.Writer
	if w.artifacts != nil {
		var err error
		artifactWriter, err = w.artifacts.NewWriterWithID(j.ID, task.Event.Type, task.Event.Repo)
		if err != nil {
			fmt.Printf("⚠️  Failed to create artifact writer: %v\n", err)
		}
	}

	// Resolve the repo path. With isolated checkouts enabled, plugin jobs run
	// against a clean checkout of the pushed sha — never the developer's live
	// working tree. Fail closed: if the checkout cannot be materialized, the
	// job fails instead of silently running against stale/dirty code.
	repoPath := ""
	if sha := event.SHAFromEvent(task.Event); sha != "" && w.reposDir != "" {
		p, err := repopath.Checkout(w.reposDir, task.Event.Repo, sha, w.checkoutRoot)
		if err != nil {
			msg := fmt.Sprintf("❌ isolated checkout failed: %v", err)
			fmt.Println(msg)
			w.pubsub.PublishLine(j.ID, msg, true)
			res := plugin.NewFailureResult(msg, task.Plugin.Name(), err)
			result := &job.ActionResult{
				Status:     res.Status,
				Summary:    res.Summary,
				FailedStep: res.FailedStep,
			}
			j.FailWithResult(err, result)
			if w.store != nil {
				_ = w.store.FinishJob(j)
			}
			w.pubsub.PublishDone(j.ID)
			return
		}
		repoPath = p
	}
	if repoPath == "" && w.repoRoot != "" {
		repoPath = repopath.Resolve(w.repoRoot, task.Event.Repo)
	}

	logWriter := func(line string, isError bool) {
		w.pubsub.PublishLine(j.ID, line, isError)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.cancels[j.ID] = cancel
	w.mu.Unlock()

	ctx := plugin.Context{
		Ctx:       runCtx,
		Event:     task.Event,
		RepoPath:  repoPath,
		Artifacts: artifactWriter,
		Env:       make(map[string]string),
		LogWriter: logWriter,
	}

	j.Start()
	j.Progress = "Running plugin..."
	if w.store != nil {
		_ = w.store.UpdateJob(j)
	}

	err := task.Plugin.Run(ctx)
	cancel()

	w.mu.Lock()
	delete(w.cancels, j.ID)
	delete(w.taskByJob, j.ID)
	wasCancelled := w.cancelled[j.ID]
	if wasCancelled {
		delete(w.cancelled, j.ID)
	}
	w.mu.Unlock()

	switch {
	case errors.Is(err, context.Canceled) || wasCancelled:
		w.finishCancelledJob(j, "Cancelled by user")
	case errors.Is(err, context.DeadlineExceeded):
		w.pubsub.PublishLine(j.ID, "❌ Error: plugin timed out", true)
		res := plugin.NewFailureResult("Plugin execution timed out", task.Plugin.Name(), err)
		result := &job.ActionResult{
			Status:     res.Status,
			Summary:    res.Summary,
			FailedStep: res.FailedStep,
		}
		j.FailWithResult(err, result)
		if w.store != nil {
			_ = w.store.FinishJob(j)
		}
		w.pubsub.PublishDone(j.ID)
	case err != nil:
		fmt.Printf("❌ Plugin %s failed: %v\n", task.Plugin.Name(), err)
		w.pubsub.PublishLine(j.ID, fmt.Sprintf("❌ Error: %v", err), true)

		// Attempt to read result.json if the plugin or exec_runner created one
		var result *job.ActionResult
		if artifactWriter != nil {
			if r, err := plugin.ReadResultFromFile(artifactWriter.Dir()); err == nil {
				result = &job.ActionResult{
					Status:     r.Status,
					Summary:    r.Summary,
					FailedStep: r.FailedStep,
					Artifacts:  r.Artifacts,
					Hints:      r.Hints,
					Details:    r.Details,
				}
				if r.Metrics != nil {
					result.Metrics = map[string]interface{}{
						"duration_ms": r.Metrics.DurationMs,
					}
				}
			}
		}

		if result == nil {
			res := plugin.NewFailureResult(fmt.Sprintf("Plugin failed: %v", err), task.Plugin.Name(), err)
			result = &job.ActionResult{
				Status:     res.Status,
				Summary:    res.Summary,
				FailedStep: res.FailedStep,
			}
		}

		j.FailWithResult(err, result)

		if w.store != nil {
			_ = w.store.FinishJob(j)
		}
		w.pubsub.PublishDone(j.ID)
	default:
		duration := time.Since(start)
		fmt.Printf("✅ Plugin %s completed in %v\n", task.Plugin.Name(), duration)
		w.pubsub.PublishLine(j.ID, fmt.Sprintf("✅ Completed in %v", duration), false)
		if artifactWriter != nil {
			fmt.Printf("   📁 Artifacts: %s\n", artifactWriter.Dir())
		}

		// Attempt to read result.json if the plugin or exec_runner created one
		var result *job.ActionResult
		if artifactWriter != nil {
			if r, err := plugin.ReadResultFromFile(artifactWriter.Dir()); err == nil {
				result = &job.ActionResult{
					Status:     r.Status,
					Summary:    r.Summary,
					FailedStep: r.FailedStep,
					Artifacts:  r.Artifacts,
					Hints:      r.Hints,
					Details:    r.Details,
				}
				if r.Metrics != nil {
					result.Metrics = map[string]interface{}{
						"duration_ms": r.Metrics.DurationMs,
					}
				}
			}
		}

		if result != nil {
			j.CompleteWithResult(result)
		} else {
			j.Complete()
		}

		if w.store != nil {
			_ = w.store.FinishJob(j)
		}
		w.pubsub.PublishDone(j.ID)
	}
}

func (w *Worker) finishCancelledJob(j *job.ActionJob, reason string) {
	j.Cancel(reason)
	j.Progress = reason
	if w.store != nil {
		_ = w.store.FinishJob(j)
	}
	w.pubsub.PublishLine(j.ID, fmt.Sprintf("🛑 %s", reason), true)
	w.pubsub.PublishDone(j.ID)
}

func (w *Worker) isCancelled(jobID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.cancelled[jobID] {
		return false
	}
	delete(w.cancelled, jobID)
	return true
}

func newJobForTask(task Task) *job.ActionJob {
	j := job.NewJob(task.Event.ID, task.Event.Repo, task.Plugin.Name())
	j.PluginName = task.Plugin.Name()
	j.PluginVersion = task.Plugin.Version() // verifier provenance (Phase B)
	j.Profile = task.Profile
	j.Intent = task.Intent
	j.Progress = "Queued"
	if eventJSON, err := json.Marshal(task.Event); err == nil {
		j.EventJSON = string(eventJSON)
	}

	shortHash := "manual"
	if len(task.Event.ID) >= 7 {
		shortHash = task.Event.ID[:7]
	}
	if hash, ok := task.Event.Payload["ref"].(string); ok && len(hash) >= 7 {
		shortHash = hash[:7]
	} else if hash, ok := task.Event.Payload["after"].(string); ok && len(hash) >= 7 {
		shortHash = hash[:7]
	}
	j.RunID = fmt.Sprintf("run-%s-%s", time.Now().Format("20060102-150405"), shortHash)

	if task.Event.Type == "git.push" {
		// LGH uses Payload.changes for pushes.
		// "changes": map[string]map[string]string{ "refs/heads/main": { "old": "...", "new": "..." } }
		if changes, ok := task.Event.Payload["changes"].(map[string]interface{}); ok {
			for _, changeInfoRaw := range changes {
				if changeInfo, ok := changeInfoRaw.(map[string]interface{}); ok {
					if newHash, ok := changeInfo["new"].(string); ok && newHash != "" {
						j.Commit.Hash = newHash
						j.CommitSHA = newHash // ensure top-level field is also set!
						break
					}
				}
			}
		}

		if hash, ok := task.Event.Payload["after"].(string); ok {
			j.Commit.Hash = hash
			j.CommitSHA = hash
		}
		if msg, ok := task.Event.Payload["message"].(string); ok {
			j.Commit.Message = msg
			j.CommitMsg = msg
		} else if headLimit, ok := task.Event.Payload["head_commit"].(map[string]interface{}); ok {
			if msg, ok := headLimit["message"].(string); ok {
				j.Commit.Message = msg
				j.CommitMsg = msg
			}
			if author, ok := headLimit["author"].(map[string]interface{}); ok {
				if name, ok := author["name"].(string); ok {
					j.Commit.Author = name
					j.CommitAuthor = name
				}
			}
		}
	}

	return j
}

func (w *Worker) taskForRetry(jobID string) (Task, error) {
	w.mu.Lock()
	task, ok := w.taskByJob[jobID]
	resolver := w.pluginResolver
	w.mu.Unlock()
	if ok {
		return task, nil
	}

	if w.store == nil {
		return Task{}, fmt.Errorf("retry task not available for job %s", jobID)
	}

	j, err := w.store.GetJob(jobID)
	if err != nil {
		return Task{}, err
	}
	if j == nil {
		return Task{}, fmt.Errorf("job %s not found", jobID)
	}
	if j.EventJSON == "" {
		return Task{}, fmt.Errorf("job %s has no persisted event snapshot", jobID)
	}
	if resolver == nil {
		return Task{}, fmt.Errorf("plugin resolver not configured")
	}

	p := resolver(j.PluginName)
	if p == nil {
		return Task{}, fmt.Errorf("plugin %s is not currently loaded", j.PluginName)
	}

	var evt event.Event
	if err := json.Unmarshal([]byte(j.EventJSON), &evt); err != nil {
		return Task{}, fmt.Errorf("decode event snapshot for %s: %w", jobID, err)
	}

	return Task{Plugin: p, Event: evt}, nil
}

// Stop gracefully stops the worker and waits for the execution goroutine to
// exit. Waiting is what lets callers (and t.TempDir cleanup in tests) safely
// remove directories after the worker has finished writing.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
	})
	w.wg.Wait()
}

// IntentForEvent extracts the task/goal intent for a git.push event by
// reading the pushed commit message via the local repo checkout
// (ASSURANCE §1 native contract v1). Returns "" when unknown — intent is
// never guessed. Prefers the isolated checkout (same dir worker jobs will
// use) so intent stays correct even when the sha is absent from the
// developer's local object store.
func IntentForEvent(repoRoot string, evt event.Event) string {
	if evt.Type != event.TypeGitPush {
		return ""
	}
	sha := event.SHAFromEvent(evt)
	if sha == "" {
		return ""
	}
	repoPath := ""
	if p, err := repopath.Checkout(repopath.DefaultReposDir(), evt.Repo, sha, ""); err == nil {
		repoPath = p
	} else {
		repoPath = repopath.Resolve(repoRoot, evt.Repo)
	}
	if repoPath == "" {
		return ""
	}
	out, err := exec.Command(gitBinary(), "-C", repoPath, "log", "-1", "--format=%s", sha).Output()
	if err != nil {
		return ""
	}
	return job.IntentFromMessage(string(out))
}

// gitBinary resolves the git executable with fallbacks: launchd-managed
// daemons run with a minimal PATH (no homebrew), so LookPath alone fails —
// same class of issue that broke the canary's go resolution.
func gitBinary() string {
	if p, err := exec.LookPath("git"); err == nil {
		return p
	}
	for _, c := range []string{"/opt/homebrew/bin/git", "/usr/local/bin/git", "/usr/bin/git"} {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	return "git"
}
