package data

import (
	"testing"
	"time"

	datapb "kwik/proto/data"
)

func TestFlowSequencer_CreateFlow(t *testing.T) {
	sequencer := NewFlowSequencer(nil)
	defer sequencer.Close()

	flowID := uint64(1)
	logicalStreamID := uint64(100)
	pathID := "path1"
	qosClass := QoSClassLowLatency

	// Create flow
	flowContext, err := sequencer.CreateFlow(flowID, logicalStreamID, pathID, qosClass)
	if err != nil {
		t.Fatalf("Failed to create flow: %v", err)
	}

	if flowContext.FlowID != flowID {
		t.Errorf("Expected flow ID %d, got %d", flowID, flowContext.FlowID)
	}

	if flowContext.LogicalStreamID != logicalStreamID {
		t.Errorf("Expected logical stream ID %d, got %d", logicalStreamID, flowContext.LogicalStreamID)
	}

	if flowContext.PathID != pathID {
		t.Errorf("Expected path ID %s, got %s", pathID, flowContext.PathID)
	}

	if flowContext.qosClass != qosClass {
		t.Errorf("Expected QoS class %v, got %v", qosClass, flowContext.qosClass)
	}

	// Try to create duplicate flow
	_, err = sequencer.CreateFlow(flowID, logicalStreamID, pathID, qosClass)
	if err == nil {
		t.Error("Expected error when creating duplicate flow")
	}

	// Check statistics
	stats := sequencer.GetSequencerStats()
	if stats.TotalFlows != 1 {
		t.Errorf("Expected 1 total flow, got %d", stats.TotalFlows)
	}

	if stats.ActiveFlows != 1 {
		t.Errorf("Expected 1 active flow, got %d", stats.ActiveFlows)
	}
}

func TestFlowSequencer_SequenceOutboundFrame(t *testing.T) {
	sequencer := NewFlowSequencer(nil)
	defer sequencer.Close()

	flowID := uint64(1)
	logicalStreamID := uint64(100)
	pathID := "path1"
	qosClass := QoSClassHighThroughput

	// Create flow
	_, err := sequencer.CreateFlow(flowID, logicalStreamID, pathID, qosClass)
	if err != nil {
		t.Fatalf("Failed to create flow: %v", err)
	}

	// Create frame
	frame := &datapb.DataFrame{
		LogicalStreamId: logicalStreamID,
		Offset:          0,
		Data:            []byte("test data"),
		PathId:          pathID,
		Timestamp:       uint64(time.Now().UnixNano()),
	}

	// Log initial frame ID
	t.Logf("Initial frame ID: %d", frame.FrameId)

	// Sequence frame
	err = sequencer.SequenceOutboundFrame(flowID, frame)
	if err != nil {
		t.Fatalf("Failed to sequence outbound frame: %v", err)
	}

	// Log frame ID after sequencing
	t.Logf("Frame ID after sequencing: %d", frame.FrameId)
	
	// Check if flow exists
	sequencer.flowsMutex.RLock()
	flowContext, exists := sequencer.flows[flowID]
	sequencer.flowsMutex.RUnlock()
	
	t.Logf("Flow exists: %v", exists)
	if exists {
		flowContext.mutex.RLock()
		nextSeq := flowContext.nextSequenceNum
		totalFrames := flowContext.totalFrames
		flowContext.mutex.RUnlock()
		t.Logf("Flow nextSequenceNum: %d, totalFrames: %d", nextSeq, totalFrames)
	}

	// Frame should have been assigned a sequence number (should be 0 since it's the first frame)
	expectedFrameId := uint64(0) // First frame should have ID 0
	if frame.FrameId != expectedFrameId {
		t.Errorf("Frame should have been assigned sequence number %d, got %d", expectedFrameId, frame.FrameId)
	}

	// Give time for background processing
	time.Sleep(50 * time.Millisecond)

	// Check statistics
	stats := sequencer.GetSequencerStats()
	if stats.TotalFramesSequenced == 0 {
		t.Error("Expected at least 1 frame to be sequenced")
	}
}

func TestFlowSequencer_ReconstituteInboundFlow(t *testing.T) {
	sequencer := NewFlowSequencer(nil)
	defer sequencer.Close()

	flowID := uint64(1)
	logicalStreamID := uint64(100)
	pathID := "path1"
	qosClass := QoSClassRealTime

	// Create flow
	_, err := sequencer.CreateFlow(flowID, logicalStreamID, pathID, qosClass)
	if err != nil {
		t.Fatalf("Failed to create flow: %v", err)
	}

	// Create frames in order
	frames := []*datapb.DataFrame{
		{
			FrameId:         0,
			LogicalStreamId: logicalStreamID,
			Offset:          0,
			Data:            []byte("Hello "),
			PathId:          pathID,
		},
		{
			FrameId:         1,
			LogicalStreamId: logicalStreamID,
			Offset:          6,
			Data:            []byte("World!"),
			PathId:          pathID,
		},
	}

	// Process frames in order
	var reconstitutedData []byte
	for _, frame := range frames {
		data, err := sequencer.ReconstituteInboundFlow(flowID, frame)
		if err != nil {
			t.Fatalf("Failed to reconstitute inbound flow: %v", err)
		}
		reconstitutedData = append(reconstitutedData, data...)
	}

	expected := "Hello World!"
	if string(reconstitutedData) != expected {
		t.Errorf("Expected reconstituted data %q, got %q", expected, string(reconstitutedData))
	}
}

func TestFlowSequencer_ReconstituteOutOfOrderFrames(t *testing.T) {
	sequencer := NewFlowSequencer(nil)
	defer sequencer.Close()

	flowID := uint64(1)
	logicalStreamID := uint64(100)
	pathID := "path1"
	qosClass := QoSClassBestEffort

	// Create flow
	_, err := sequencer.CreateFlow(flowID, logicalStreamID, pathID, qosClass)
	if err != nil {
		t.Fatalf("Failed to create flow: %v", err)
	}

	// Create frames out of order
	frames := []*datapb.DataFrame{
		{
			FrameId:         1, // Second frame first
			LogicalStreamId: logicalStreamID,
			Offset:          6,
			Data:            []byte("World!"),
			PathId:          pathID,
		},
		{
			FrameId:         0, // First frame second
			LogicalStreamId: logicalStreamID,
			Offset:          0,
			Data:            []byte("Hello "),
			PathId:          pathID,
		},
	}

	// Process frames out of order
	var allData []byte
	for _, frame := range frames {
		data, err := sequencer.ReconstituteInboundFlow(flowID, frame)
		if err != nil {
			t.Fatalf("Failed to reconstitute inbound flow: %v", err)
		}
		allData = append(allData, data...)
	}

	expected := "Hello World!"
	if string(allData) != expected {
		t.Errorf("Expected reconstituted data %q, got %q", expected, string(allData))
	}

	// Check that frames were reordered
	stats := sequencer.GetSequencerStats()
	if stats.FramesReordered == 0 {
		t.Error("Expected some frames to be reordered")
	}
}

func TestSequencingQueue_PriorityOrdering(t *testing.T) {
	queue := NewSequencingQueue()

	// Create frames with different priorities
	frames := []*SequencedFrame{
		{
			Frame: &datapb.DataFrame{
				Data: []byte("low priority"),
			},
			Priority:    10,
			QoS:         QoSClassBestEffort,
			EnqueueTime: time.Now(),
		},
		{
			Frame: &datapb.DataFrame{
				Data: []byte("high priority"),
			},
			Priority:    100,
			QoS:         QoSClassCritical,
			EnqueueTime: time.Now(),
		},
		{
			Frame: &datapb.DataFrame{
				Data: []byte("medium priority"),
			},
			Priority:    50,
			QoS:         QoSClassLowLatency,
			EnqueueTime: time.Now(),
		},
	}

	// Enqueue frames
	for _, frame := range frames {
		queue.Enqueue(frame)
	}

	// Dequeue frames - should come out in priority order
	frame1 := queue.Dequeue()
	if frame1 == nil {
		t.Fatal("Expected to dequeue high priority frame")
	}
	if string(frame1.Frame.Data) != "high priority" {
		t.Errorf("Expected high priority frame, got %s", string(frame1.Frame.Data))
	}

	frame2 := queue.Dequeue()
	if frame2 == nil {
		t.Fatal("Expected to dequeue medium priority frame")
	}
	if string(frame2.Frame.Data) != "medium priority" {
		t.Errorf("Expected medium priority frame, got %s", string(frame2.Frame.Data))
	}

	frame3 := queue.Dequeue()
	if frame3 == nil {
		t.Fatal("Expected to dequeue low priority frame")
	}
	if string(frame3.Frame.Data) != "low priority" {
		t.Errorf("Expected low priority frame, got %s", string(frame3.Frame.Data))
	}

	// Queue should be empty now
	if queue.Size() != 0 {
		t.Errorf("Expected empty queue, got size %d", queue.Size())
	}
}

func TestSequencingQueue_QoSFiltering(t *testing.T) {
	queue := NewSequencingQueue()

	// Create frames with different QoS classes
	frames := []*SequencedFrame{
		{
			Frame: &datapb.DataFrame{Data: []byte("best effort")},
			QoS:   QoSClassBestEffort,
		},
		{
			Frame: &datapb.DataFrame{Data: []byte("low latency")},
			QoS:   QoSClassLowLatency,
		},
		{
			Frame: &datapb.DataFrame{Data: []byte("real time")},
			QoS:   QoSClassRealTime,
		},
		{
			Frame: &datapb.DataFrame{Data: []byte("another low latency")},
			QoS:   QoSClassLowLatency,
		},
	}

	// Enqueue frames
	for _, frame := range frames {
		queue.Enqueue(frame)
	}

	// Get frames by QoS class
	lowLatencyFrames := queue.GetFramesByQoS(QoSClassLowLatency)
	if len(lowLatencyFrames) != 2 {
		t.Errorf("Expected 2 low latency frames, got %d", len(lowLatencyFrames))
	}

	realTimeFrames := queue.GetFramesByQoS(QoSClassRealTime)
	if len(realTimeFrames) != 1 {
		t.Errorf("Expected 1 real time frame, got %d", len(realTimeFrames))
	}

	criticalFrames := queue.GetFramesByQoS(QoSClassCritical)
	if len(criticalFrames) != 0 {
		t.Errorf("Expected 0 critical frames, got %d", len(criticalFrames))
	}
}

func TestFlowBuffer_ReadWrite(t *testing.T) {
	buffer := NewFlowBuffer(100)

	// Test write
	testData := []byte("hello world")
	n, err := buffer.Write(testData)
	if err != nil {
		t.Fatalf("Failed to write to buffer: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(testData), n)
	}

	// Test read
	readBuffer := make([]byte, len(testData))
	n, err = buffer.Read(readBuffer)
	if err != nil {
		t.Fatalf("Failed to read from buffer: %v", err)
	}
	if n != len(testData) {
		t.Errorf("Expected to read %d bytes, read %d", len(testData), n)
	}
	if string(readBuffer) != string(testData) {
		t.Errorf("Expected to read %q, read %q", string(testData), string(readBuffer))
	}

	// Test capacity limit
	largeData := make([]byte, 200)
	_, err = buffer.Write(largeData)
	if err == nil {
		t.Error("Expected error when exceeding buffer capacity")
	}
}

func TestFlowReconstitutionEngine_BasicReconstitution(t *testing.T) {
	sequencer := NewFlowSequencer(nil)
	defer sequencer.Close()

	engine := NewFlowReconstitutionEngine(sequencer, nil)
	flowID := uint64(1)

	// Create frames out of order
	frames := []*datapb.DataFrame{
		{
			Offset: 6,
			Data:   []byte("World!"),
		},
		{
			Offset: 0,
			Data:   []byte("Hello "),
		},
	}

	// Reconstitute flow
	data, err := engine.basicReconstitution(flowID, frames)
	if err != nil {
		t.Fatalf("Failed to reconstitute flow: %v", err)
	}

	expected := "Hello World!"
	if string(data) != expected {
		t.Errorf("Expected reconstituted data %q, got %q", expected, string(data))
	}
}

func TestFlowReconstitutionEngine_PredictiveReconstitution(t *testing.T) {
	sequencer := NewFlowSequencer(nil)
	defer sequencer.Close()

	config := &ReconstitutionConfig{
		EnablePredictiveOrdering: true,
	}
	engine := NewFlowReconstitutionEngine(sequencer, config)
	flowID := uint64(1)

	// Create frames out of order
	frames := []*datapb.DataFrame{
		{
			Offset: 12,
			Data:   []byte(" predictive!"),
		},
		{
			Offset: 0,
			Data:   []byte("Hello"),
		},
		{
			Offset: 5,
			Data:   []byte(" World"),
		},
	}

	// Reconstitute flow with prediction
	data, err := engine.ReconstituteFlowWithPrediction(flowID, frames)
	if err != nil {
		t.Fatalf("Failed to reconstitute flow with prediction: %v", err)
	}

	expected := "Hello World predictive!"
	if string(data) != expected {
		t.Errorf("Expected reconstituted data %q, got %q", expected, string(data))
	}
}

func TestQoSClass_String(t *testing.T) {
	testCases := []struct {
		qos      QoSClass
		expected string
	}{
		{QoSClassBestEffort, "BEST_EFFORT"},
		{QoSClassLowLatency, "LOW_LATENCY"},
		{QoSClassHighThroughput, "HIGH_THROUGHPUT"},
		{QoSClassRealTime, "REAL_TIME"},
		{QoSClassCritical, "CRITICAL"},
	}

	for _, tc := range testCases {
		if tc.qos.String() != tc.expected {
			t.Errorf("Expected QoS string %s, got %s", tc.expected, tc.qos.String())
		}
	}
}

func TestSequencingTaskType_String(t *testing.T) {
	testCases := []struct {
		taskType SequencingTaskType
		expected string
	}{
		{TaskTypeSequenceFrame, "SEQUENCE_FRAME"},
		{TaskTypeReconstituteFlow, "RECONSTITUTE_FLOW"},
		{TaskTypeOptimizeSequencing, "OPTIMIZE_SEQUENCING"},
		{TaskTypeFlowCleanup, "FLOW_CLEANUP"},
	}

	for _, tc := range testCases {
		if tc.taskType.String() != tc.expected {
			t.Errorf("Expected task type string %s, got %s", tc.expected, tc.taskType.String())
		}
	}
}