package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
)

type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

type harness struct {
	baseURL string
	client  *http.Client
	server  *httptest.Server
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to verify")
	flag.Parse()

	checks := map[string]func() error{
		"register-device":          checkRegisterDevice,
		"open-verification-case":   checkOpenVerificationCase,
		"record-measurement":       checkRecordMeasurement,
		"replace-measurement":      checkReplaceMeasurement,
		"reopen-verification-case": checkReopenVerificationCase,
		"seal-verification":        checkSealVerification,
	}
	run, ok := checks[*workflow]
	if !ok {
		fmt.Fprintf(flag.CommandLine.Output(), "unknown workflow %q\n", *workflow)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Printf("workflow %s: failed: %v\n", *workflow, err)
		os.Exit(1)
	}
	fmt.Printf("workflow %s: ok\n", *workflow)
}

func checkRegisterDevice() error {
	h := newHarness()
	defer h.close()

	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": " met-1001 ",
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return err
	}
	id, err := nestedString(response.Body, "device", "id")
	if err != nil {
		return err
	}
	tag, err := nestedString(response.Body, "device", "asset_tag")
	if err != nil {
		return err
	}
	if tag != "MET-1001" {
		return fmt.Errorf("asset tag was not normalized: %q", tag)
	}

	read, err := h.request(http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readID, err := nestedString(read.Body, "device", "id")
	if err != nil {
		return err
	}
	if readID != id {
		return fmt.Errorf("read returned device %q, expected %q", readID, id)
	}

	duplicate, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": "MET-1001",
		"model":     "Other Meter",
		"room":      "Room B",
		"steward":   "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(duplicate, "duplicate_asset_tag")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(opened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("opened case state = %q", state)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(second, "active_case_exists")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(accepted.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("accepted measurement state = %q", state)
	}
	acceptedValue, err := nestedBool(accepted.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if !acceptedValue {
		return errors.New("in-tolerance measurement was not accepted")
	}

	second, err := h.recordMeasurement(caseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	rejectDeviceID, err := h.createDevice("MET-1202")
	if err != nil {
		return err
	}
	rejectCase, err := h.openCase(rejectDeviceID)
	if err != nil {
		return err
	}
	rejectCaseID, err := nestedString(rejectCase.Body, "case", "id")
	if err != nil {
		return err
	}
	rejected, err := h.recordMeasurement(rejectCaseID, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	rejectedState, err := nestedString(rejected.Body, "case", "state")
	if err != nil {
		return err
	}
	if rejectedState != "recheck_required" {
		return fmt.Errorf("out-of-tolerance state = %q", rejectedState)
	}
	rejectedValue, err := nestedBool(rejected.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if rejectedValue {
		return errors.New("out-of-tolerance measurement was accepted")
	}
	return nil
}

func checkReplaceMeasurement() error {
	// Case 1: an out-of-tolerance reading is corrected to an in-tolerance one.
	// The case must leave recheck_required and become ready to seal, and the
	// resulting seal must cite the new reading.
	h := newHarness()
	defer h.close()

	caseID, err := h.rejectedCase("MET-1501")
	if err != nil {
		return err
	}
	badRead, err := h.getCase(caseID)
	if err != nil {
		return err
	}
	badID, err := nestedString(badRead.Body, "measurements", 0, "id")
	if err != nil {
		return err
	}

	corrected, err := h.replaceMeasurement(caseID, 25.1, "Rui", badID)
	if err != nil {
		return err
	}
	if err := expectStatus(corrected, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(corrected.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("rejected->accepted replacement state = %q, want review_ready", state)
	}
	measurements, err := nestedSlice(corrected.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 2 {
		return fmt.Errorf("replacement removed history: %d readings, want 2", len(measurements))
	}
	if err := expectOnlyCurrentReading(corrected.Body); err != nil {
		return err
	}
	goodID, err := nestedString(corrected.Body, "measurements", 1, "id")
	if err != nil {
		return err
	}
	oldSuperseded, err := nestedString(corrected.Body, "measurements", 0, "superseded_by")
	if err != nil {
		return err
	}
	if oldSuperseded != goodID {
		return fmt.Errorf("old reading superseded_by = %q, want %q", oldSuperseded, goodID)
	}
	newReplaces, err := nestedString(corrected.Body, "measurements", 1, "replaces")
	if err != nil {
		return err
	}
	if newReplaces != badID {
		return fmt.Errorf("new reading replaces = %q, want %q", newReplaces, badID)
	}
	activeID, err := nestedString(corrected.Body, "case", "active_measurement_id")
	if err != nil {
		return err
	}
	if activeID != goodID {
		return fmt.Errorf("active_measurement_id = %q, want %q", activeID, goodID)
	}
	if !hasEvent(corrected.Body, "measurement_replaced") {
		return errors.New("replacement audit event is missing")
	}

	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Verifier",
		"summary":  "corrected reading sealed",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	sealedMeasurement, err := nestedString(sealed.Body, "seal", "measurement_id")
	if err != nil {
		return err
	}
	if sealedMeasurement != goodID {
		return fmt.Errorf("seal cites %q, want current reading %q", sealedMeasurement, goodID)
	}
	// The replaced-away reading must still be fully present in the sealed record.
	if err := expectOnlyCurrentReading(sealed.Body); err != nil {
		return err
	}
	replacedSealAfter, err := h.replaceMeasurement(caseID, 25.0, "Rui", goodID)
	if err != nil {
		return err
	}
	if err := expectStatus(replacedSealAfter, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(replacedSealAfter, "state_conflict"); err != nil {
		return err
	}

	// Case 2: an accepted reading is "corrected" to an out-of-tolerance one.
	// The case must leave review_ready and refuse to seal.
	h2 := newHarness()
	defer h2.close()

	caseID2, err := h2.readyCase("MET-1502")
	if err != nil {
		return err
	}
	goodRead, err := h2.getCase(caseID2)
	if err != nil {
		return err
	}
	originalID, err := nestedString(goodRead.Body, "measurements", 0, "id")
	if err != nil {
		return err
	}
	degraded, err := h2.replaceMeasurement(caseID2, 26.5, "Rui", originalID)
	if err != nil {
		return err
	}
	if err := expectStatus(degraded, http.StatusCreated); err != nil {
		return err
	}
	state2, err := nestedString(degraded.Body, "case", "state")
	if err != nil {
		return err
	}
	if state2 != "recheck_required" {
		return fmt.Errorf("accepted->rejected replacement state = %q, want recheck_required", state2)
	}
	if err := expectOnlyCurrentReading(degraded.Body); err != nil {
		return err
	}
	sealRejected, err := h2.request(http.MethodPost, "/cases/"+caseID2+"/seal", map[string]any{
		"reviewer": "Verifier",
		"summary":  "must not seal a recheck case",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealRejected, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(sealRejected, "state_conflict"); err != nil {
		return err
	}

	// Case 3: a replacement that does not hold must leave the case untouched.
	h3 := newHarness()
	defer h3.close()

	caseID3, err := h3.readyCase("MET-1503")
	if err != nil {
		return err
	}
	before, err := h3.getCase(caseID3)
	if err != nil {
		return err
	}
	beforeVersion, err := nestedFloat(before.Body, "case", "version")
	if err != nil {
		return err
	}
	beforeRead, err := nestedString(before.Body, "measurements", 0, "id")
	if err != nil {
		return err
	}

	missing, err := h3.replaceMeasurement(caseID3, 25.0, "Rui", "measurement-9999")
	if err != nil {
		return err
	}
	if err := expectStatus(missing, http.StatusNotFound); err != nil {
		return err
	}
	invalid, err := h3.request(http.MethodPost, "/cases/"+caseID3+"/measurements", map[string]any{
		"observed":   25.0,
		"technician": "   ",
		"replaces":   beforeRead,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(invalid, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(invalid, "invalid_input"); err != nil {
		return err
	}
	after, err := h3.getCase(caseID3)
	if err != nil {
		return err
	}
	afterVersion, err := nestedFloat(after.Body, "case", "version")
	if err != nil {
		return err
	}
	if afterVersion != beforeVersion {
		return fmt.Errorf("rejected replacement changed case version: %v -> %v", beforeVersion, afterVersion)
	}
	afterState, err := nestedString(after.Body, "case", "state")
	if err != nil {
		return err
	}
	if afterState != "review_ready" {
		return fmt.Errorf("rejected replacement changed case state to %q", afterState)
	}
	afterMeasurements, err := nestedSlice(after.Body, "measurements")
	if err != nil {
		return err
	}
	if len(afterMeasurements) != 1 {
		return fmt.Errorf("rejected replacement left %d readings, want 1", len(afterMeasurements))
	}
	if afterMeasurements[0].(map[string]any)["id"] != beforeRead {
		return errors.New("rejected replacement changed the current reading")
	}
	if marked, _ := afterMeasurements[0].(map[string]any)["superseded_by"].(string); marked != "" {
		return fmt.Errorf("rejected replacement marked the reading superseded: %q", marked)
	}

	// Case 4: replacement and seal submitted simultaneously must always settle
	// in one coherent result: at most one seal, and if a seal exists it cites
	// the single current reading. Repeated for both acceptance directions and
	// many iterations because the repository lock serializes the two commits in
	// either order.
	cases := []struct {
		tag      string
		observed float64
		accepted bool
	}{
		{"MET-1510", 24.9, true},
		{"MET-1530", 26.5, false},
	}
	for _, scenario := range cases {
		for attempt := 0; attempt < 10; attempt++ {
			hr := newHarness()
			caseIDr, err := hr.readyCase(fmt.Sprintf("%s-%02d", scenario.tag, attempt))
			if err != nil {
				hr.close()
				return err
			}
			currentRead, err := hr.getCase(caseIDr)
			if err != nil {
				hr.close()
				return err
			}
			replaceID, err := nestedString(currentRead.Body, "measurements", 0, "id")
			if err != nil {
				hr.close()
				return err
			}
			type outcome struct {
				response apiResponse
				err      error
			}
			results := make(chan outcome, 2)
			var wait sync.WaitGroup
			wait.Add(2)
			go func() {
				defer wait.Done()
				response, err := hr.request(http.MethodPost, "/cases/"+caseIDr+"/seal", map[string]any{
					"reviewer": "Verifier",
					"summary":  "seal raced against replacement",
				})
				results <- outcome{response: response, err: err}
			}()
			go func() {
				defer wait.Done()
				response, err := hr.replaceMeasurement(caseIDr, scenario.observed, "Rui", replaceID)
				results <- outcome{response: response, err: err}
			}()
			wait.Wait()
			close(results)

			sealCreated := 0
			replacementCreated := 0
			for item := range results {
				if item.err != nil {
					hr.close()
					return item.err
				}
				if item.response.Status != http.StatusCreated && item.response.Status != http.StatusConflict {
					hr.close()
					return fmt.Errorf("raced operation returned status %d: %s", item.response.Status, item.response.Raw)
				}
				if item.response.Status != http.StatusCreated {
					continue
				}
				if _, hasSeal := item.response.Body["seal"]; hasSeal {
					sealCreated++
				} else {
					replacementCreated++
				}
			}
			if sealCreated > 1 {
				hr.close()
				return fmt.Errorf("%s attempt %d: %d seals were created", scenario.tag, attempt, sealCreated)
			}
			if replacementCreated > 1 {
				hr.close()
				return fmt.Errorf("%s attempt %d: replacement applied more than once", scenario.tag, attempt)
			}
			if sealCreated == 0 && replacementCreated == 0 {
				hr.close()
				return fmt.Errorf("%s attempt %d: both raced operations were rejected", scenario.tag, attempt)
			}
			final, err := hr.getCase(caseIDr)
			hr.close()
			if err != nil {
				return err
			}
			if err := expectCoherentFinalState(final.Body, scenario.accepted); err != nil {
				return fmt.Errorf("%s attempt %d: %w", scenario.tag, attempt, err)
			}
		}
	}
	return nil
}

func expectOnlyCurrentReading(body map[string]any) error {
	measurements, err := nestedSlice(body, "measurements")
	if err != nil {
		return err
	}
	current := 0
	var currentID string
	for _, item := range measurements {
		reading, ok := item.(map[string]any)
		if !ok {
			return errors.New("measurement entry is not an object")
		}
		flag, _ := reading["current"].(bool)
		if flag {
			current++
			currentID, _ = reading["id"].(string)
			if marked, _ := reading["superseded_by"].(string); marked != "" {
				return errors.New("the current reading is marked superseded")
			}
			continue
		}
		if marked, _ := reading["superseded_by"].(string); marked == "" {
			return errors.New("an archived reading is not marked superseded")
		}
	}
	if current != 1 {
		return fmt.Errorf("expected exactly one current reading, found %d", current)
	}
	activeID, err := nestedString(body, "case", "active_measurement_id")
	if err != nil {
		return err
	}
	if activeID != currentID {
		return fmt.Errorf("active_measurement_id %q does not match current reading %q", activeID, currentID)
	}
	return nil
}

func expectCoherentFinalState(body map[string]any, replacementAccepted bool) error {
	state, err := nestedString(body, "case", "state")
	if err != nil {
		return err
	}
	measurements, err := nestedSlice(body, "measurements")
	if err != nil {
		return err
	}
	currentCount := 0
	var currentReading map[string]any
	for _, item := range measurements {
		reading, ok := item.(map[string]any)
		if !ok {
			return errors.New("measurement entry is not an object")
		}
		if marked, _ := reading["superseded_by"].(string); marked == "pending-replacement" {
			return errors.New("a reading is left in a pending replacement state")
		}
		if flag, _ := reading["current"].(bool); flag {
			currentCount++
			currentReading = reading
		}
	}
	if currentCount != 1 {
		return fmt.Errorf("final state has %d current readings, want 1", currentCount)
	}
	activeID, err := nestedString(body, "case", "active_measurement_id")
	if err != nil {
		return err
	}
	currentID, _ := currentReading["id"].(string)
	if activeID != currentID {
		return fmt.Errorf("active_measurement_id %q does not match current reading %q", activeID, currentID)
	}
	sealBody, sealExists := body["seal"].(map[string]any)
	switch state {
	case "sealed":
		if !sealExists {
			return errors.New("sealed case has no evidence seal")
		}
		sealedID, _ := sealBody["measurement_id"].(string)
		if sealedID != currentID {
			return fmt.Errorf("seal cites %q while current reading is %q", sealedID, currentID)
		}
		if accepted, _ := currentReading["accepted"].(bool); !accepted {
			return errors.New("sealed case is backed by an out-of-tolerance reading")
		}
	case "recheck_required":
		if replacementAccepted {
			return errors.New("in-tolerance replacement settled in recheck_required")
		}
		if sealExists {
			return errors.New("a recheck_required case carries an evidence seal")
		}
		if accepted, _ := currentReading["accepted"].(bool); accepted {
			return errors.New("recheck_required case is backed by an accepted reading")
		}
	default:
		return fmt.Errorf("raced outcome settled in unexpected state %q", state)
	}
	return nil
}

func checkReopenVerificationCase() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.rejectedCase("MET-1301")
	if err != nil {
		return err
	}
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "measurement setup was replaced",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("reopened case state = %q", state)
	}
	measurements, err := nestedSlice(reopened.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 1 {
		return fmt.Errorf("reopen changed measurement history: %d entries", len(measurements))
	}

	second, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "duplicate request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_reopened") {
		return errors.New("reopen audit event is missing")
	}
	return nil
}

func checkSealVerification() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCase("MET-1401")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
				"reviewer": fmt.Sprintf("Reviewer-%d", index+1),
				"summary":  "accepted measurement sealed for review",
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent seal status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent seal outcomes: %d success, %d conflict", successes, conflicts)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "sealed" {
		return fmt.Errorf("sealed case state = %q", state)
	}
	sealID, err := nestedString(read.Body, "seal", "id")
	if err != nil {
		return err
	}
	if sealID == "" {
		return errors.New("evidence seal is missing")
	}
	return nil
}

func newHarness() *harness {
	server := httptest.NewServer(bootstrap.NewHandler())
	return &harness{
		baseURL: server.URL,
		client:  server.Client(),
		server:  server,
	}
}

func (h *harness) close() {
	h.server.Close()
}

func (h *harness) request(method, path string, body any) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return apiResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{Status: response.StatusCode, Raw: string(rawBytes)}
	if len(bytes.TrimSpace(rawBytes)) > 0 {
		if err := json.Unmarshal(rawBytes, &result.Body); err != nil {
			return apiResponse{}, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return result, nil
}

func (h *harness) createDevice(tag string) (string, error) {
	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": tag,
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "device", "id")
}

func (h *harness) openCase(deviceID string) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return apiResponse{}, err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return apiResponse{}, err
	}
	return response, nil
}

func (h *harness) recordMeasurement(caseID string, observed float64, technician string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"technician": technician,
	})
}

func (h *harness) replaceMeasurement(caseID string, observed float64, technician, replaces string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"technician": technician,
		"replaces":   replaces,
	})
}

func (h *harness) getCase(caseID string) (apiResponse, error) {
	return h.request(http.MethodGet, "/cases/"+caseID, nil)
}

func (h *harness) rejectedCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func expectStatus(response apiResponse, expected int) error {
	if response.Status != expected {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, expected, response.Raw)
	}
	return nil
}

func expectErrorCode(response apiResponse, expected string) error {
	code, err := nestedString(response.Body, "error", "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code = %q, expected %q", code, expected)
	}
	return nil
}

func nestedString(root map[string]any, path ...any) (string, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%v is not a string", path)
	}
	return text, nil
}

func nestedBool(root map[string]any, path ...any) (bool, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return false, err
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a boolean", path)
	}
	return flag, nil
}

func nestedFloat(root map[string]any, path ...string) (float64, error) {
	value, err := nestedValue(root, path...)
	if err != nil {
		return 0, err
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("%s is not a number", strings.Join(path, "."))
	}
	return number, nil
}

func nestedSlice(root map[string]any, key string) ([]any, error) {
	value, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return items, nil
}

func hasEvent(root map[string]any, kind string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			return true
		}
	}
	return false
}

func nestedValue(root map[string]any, path ...string) (any, error) {
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedAny(root, parts...)
}

func nestedAny(root map[string]any, path ...any) (any, error) {
	var current any = root
	for _, part := range path {
		switch typed := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", typed)
			}
			value, ok := object[typed]
			if !ok {
				return nil, fmt.Errorf("missing %s", typed)
			}
			current = value
		case int:
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("index %d is not inside an array", typed)
			}
			if typed < 0 || typed >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", typed)
			}
			current = items[typed]
		default:
			return nil, fmt.Errorf("unsupported path element %T", part)
		}
	}
	return current, nil
}
