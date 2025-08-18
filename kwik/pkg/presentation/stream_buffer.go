package presentation

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// StreamBufferImpl implements the StreamBuffer interface
type StreamBufferImpl struct {
	// Basic properties
	streamID uint64
	metadata *StreamMetadata
	config   *StreamBufferConfig

	// Buffer data storage - using map for efficient offset-based access
	dataChunks  map[uint64]*DataChunk // offset -> data chunk
	chunksMutex sync.RWMutex

	// Read/write positions
	readPosition  uint64
	writePosition uint64
	positionMutex sync.RWMutex

	// Buffer state
	totalSize uint64
	usedSize  uint64
	sizeMutex sync.RWMutex

	// Gap tracking for efficient contiguous reading
	gaps      map[uint64]uint64 // start_offset -> end_offset
	gapsMutex sync.RWMutex

	// Statistics
	stats      *DetailedStreamStats
	statsMutex sync.RWMutex

	// Lifecycle
	closed     bool
	closeMutex sync.RWMutex

	// Callbacks and notifications
	backpressureCallback func(streamID uint64, needed bool)

	// Readiness signaling (notify when contiguous data becomes available)
	readyCh        chan struct{}
	readyMutex     sync.Mutex
	hasReadySignal bool
}

// DataChunk represents a chunk of data with metadata
type DataChunk struct {
	Offset    uint64
	Data      []byte
	Metadata  *DataMetadata
	Timestamp time.Time
}

// NewStreamBuffer creates a new stream buffer with the given configuration
func NewStreamBuffer(streamID uint64, metadata *StreamMetadata, config *StreamBufferConfig) *StreamBufferImpl {
	if config == nil {
		config = &StreamBufferConfig{
			StreamID:      streamID,
			BufferSize:    DefaultStreamBufferSize,
			Priority:      StreamPriorityNormal,
			GapTimeout:    DefaultGapTimeout,
			EnableMetrics: true,
		}
	}

	buffer := &StreamBufferImpl{
		streamID:      streamID,
		metadata:      metadata,
		config:        config,
		dataChunks:    make(map[uint64]*DataChunk),
		gaps:          make(map[uint64]uint64),
		readPosition:  0,
		writePosition: 0,
		totalSize:     config.BufferSize,
		usedSize:      0,
		closed:        false,
		stats: &DetailedStreamStats{
			StreamStats: StreamStats{
				StreamID:           streamID,
				BytesWritten:       0,
				BytesRead:          0,
				WriteOperations:    0,
				ReadOperations:     0,
				GapEvents:          0,
				BackpressureEvents: 0,
				AverageLatency:     0,
				LastActivity:       time.Now(),
			},
			WriteLatencyHistogram:    make(map[time.Duration]uint64),
			ReadLatencyHistogram:     make(map[time.Duration]uint64),
			GapSizeHistogram:         make(map[uint64]uint64),
			BufferUtilizationHistory: make([]float64, 0, 100), // Keep last 100 measurements
			MaxBufferUtilization:     0,
			MinBufferUtilization:     1.0,
			BackpressureDuration:     0,
			BackpressureFrequency:    0,
			OutOfOrderPackets:        0,
			DuplicatePackets:         0,
			CorruptedPackets:         0,
		},
		readyCh:        make(chan struct{}, 1),
		hasReadySignal: false,
	}

	return buffer
}

// Write writes data at the specified offset
func (sb *StreamBufferImpl) Write(data []byte, offset uint64) error {
	return sb.WriteWithMetadata(data, offset, &DataMetadata{
		Offset:    offset,
		Length:    uint64(len(data)),
		Timestamp: time.Now(),
		Flags:     DataFlagNone,
	})
}

// WriteWithMetadata writes data with associated metadata at the specified offset
func (sb *StreamBufferImpl) WriteWithMetadata(data []byte, offset uint64, metadata *DataMetadata) error {
	startTime := time.Now()

	sb.closeMutex.RLock()
	if sb.closed {
		sb.closeMutex.RUnlock()
		return fmt.Errorf("stream buffer %d is closed", sb.streamID)
	}
	sb.closeMutex.RUnlock()

	if len(data) == 0 {
		return fmt.Errorf("cannot write empty data")
	}

	// Check if we have enough space, considering consumed data that can be overwritten
	sb.sizeMutex.Lock()
	consumedSpace := sb.getConsumedSpaceSize()
	var effectiveUsedSize uint64
	if sb.usedSize > consumedSpace {
		effectiveUsedSize = sb.usedSize - consumedSpace
	} else {
		effectiveUsedSize = 0 // All used space is consumed, so effectively 0
	}
	if effectiveUsedSize+uint64(len(data)) > sb.totalSize {
		sb.sizeMutex.Unlock()
		// Trigger backpressure
		if sb.backpressureCallback != nil {
			sb.backpressureCallback(sb.streamID, true)
		}
		return fmt.Errorf("buffer full: cannot write %d bytes (effective used: %d, consumed: %d, total: %d)",
			len(data), effectiveUsedSize, consumedSpace, sb.totalSize)
	}

	// Only increase usedSize if we're not overwriting consumed data
	if consumedSpace >= uint64(len(data)) {
		// We're overwriting consumed data, don't increase usedSize
		// fmt.Printf("DEBUG: Stream %d overwriting consumed data: dataSize=%d, consumedSpace=%d, usedSize stays %d\n",
		// 	sb.streamID, len(data), consumedSpace, sb.usedSize)
	} else {
		// We're writing beyond consumed data, increase usedSize by the difference
		increase := uint64(len(data)) - consumedSpace
		sb.usedSize += increase
		fmt.Printf("DEBUG: Stream %d writing new data: dataSize=%d, consumedSpace=%d, increase=%d, newUsedSize=%d\n",
			sb.streamID, len(data), consumedSpace, increase, sb.usedSize)
	}
	sb.sizeMutex.Unlock()

	// Create data chunk
	chunk := &DataChunk{
		Offset:    offset,
		Data:      make([]byte, len(data)),
		Metadata:  metadata,
		Timestamp: time.Now(),
	}
	copy(chunk.Data, data)

	// Check for duplicate writes
	sb.chunksMutex.Lock()
	if existingChunk, exists := sb.dataChunks[offset]; exists {
		sb.chunksMutex.Unlock()

		// Update statistics for duplicate
		sb.statsMutex.Lock()
		sb.stats.DuplicatePackets++
		sb.statsMutex.Unlock()

		// If it's the same data, ignore silently
		if len(existingChunk.Data) == len(data) {
			same := true
			for i, b := range data {
				if existingChunk.Data[i] != b {
					same = false
					break
				}
			}
			if same {
				return nil // Duplicate but identical data
			}
		}

		return fmt.Errorf("duplicate write at offset %d with different data", offset)
	}

	// Store the chunk
	sb.dataChunks[offset] = chunk
	sb.chunksMutex.Unlock()

	// Update write position if this extends the buffer
	sb.positionMutex.Lock()
	endOffset := offset + uint64(len(data))
	if endOffset > sb.writePosition {
		sb.writePosition = endOffset
	}
	sb.positionMutex.Unlock()

	// Update gaps
	sb.updateGapsAfterWrite(offset, uint64(len(data)))

	// Signal readiness if new contiguous data is available at current read position
	sb.maybeSignalReady()

	// Update statistics
	sb.updateWriteStats(len(data), time.Since(startTime))

	return nil
}

// Read reads available contiguous data into the buffer
func (sb *StreamBufferImpl) Read(buffer []byte) (int, error) {
	return sb.ReadContiguous(buffer)
}

// ReadContiguous reads only contiguous data (stops at first gap)
func (sb *StreamBufferImpl) ReadContiguous(buffer []byte) (int, error) {
	startTime := time.Now()

	sb.closeMutex.RLock()
	if sb.closed {
		sb.closeMutex.RUnlock()
		return 0, fmt.Errorf("stream buffer %d is closed", sb.streamID)
	}
	sb.closeMutex.RUnlock()

	if len(buffer) == 0 {
		return 0, fmt.Errorf("buffer cannot be empty")
	}

	sb.positionMutex.RLock()
	currentReadPos := sb.readPosition
	sb.positionMutex.RUnlock()

	// Find contiguous data starting from read position
	contiguousData := sb.getContiguousDataFromPosition(currentReadPos, len(buffer))

	if len(contiguousData) == 0 {
		// No contiguous data available
		sb.updateReadStats(0, time.Since(startTime))
		return 0, nil
	}

	// Copy data to buffer
	n := copy(buffer, contiguousData)

	// Advance read position
	sb.positionMutex.Lock()
	sb.readPosition += uint64(n)
	sb.positionMutex.Unlock()

	// Mark data as consumed to free up buffer space for new writes
	// but keep the data available for re-reading
	sb.markDataAsConsumed(currentReadPos, uint64(n))

	// Update statistics
	sb.updateReadStats(n, time.Since(startTime))

	return n, nil
}

// WaitReady blocks until contiguous data is available or timeout elapses
func (sb *StreamBufferImpl) WaitReady(timeout time.Duration) bool {
	// Fast-path: check if data is already available
	sb.positionMutex.RLock()
	currentReadPos := sb.readPosition
	sb.positionMutex.RUnlock()
	if len(sb.getContiguousDataFromPosition(currentReadPos, 1)) > 0 {
		return true
	}

	if timeout <= 0 {
		return false
	}

	select {
	case <-sb.readyCh:
		// Reset signal state
		sb.readyMutex.Lock()
		sb.hasReadySignal = false
		sb.readyMutex.Unlock()
		return true
	case <-time.After(timeout):
		return false
	}
}

// maybeSignalReady sends a readiness signal if contiguous data becomes available
func (sb *StreamBufferImpl) maybeSignalReady() {
	// Check contiguous availability at current read position
	sb.positionMutex.RLock()
	currentReadPos := sb.readPosition
	sb.positionMutex.RUnlock()
	if len(sb.getContiguousDataFromPosition(currentReadPos, 1)) == 0 {
		return
	}

	// Non-blocking send if no pending signal
	sb.readyMutex.Lock()
	defer sb.readyMutex.Unlock()
	if sb.hasReadySignal {
		return
	}
	select {
	case sb.readyCh <- struct{}{}:
		sb.hasReadySignal = true
	default:
		// Channel already has a signal
		sb.hasReadySignal = true
	}
}

// GetReadPosition returns the current read position
func (sb *StreamBufferImpl) GetReadPosition() uint64 {
	sb.positionMutex.RLock()
	defer sb.positionMutex.RUnlock()
	return sb.readPosition
}

// SetReadPosition sets the read position (use with caution)
func (sb *StreamBufferImpl) SetReadPosition(position uint64) error {
	sb.positionMutex.Lock()
	defer sb.positionMutex.Unlock()

	if position > sb.writePosition {
		return fmt.Errorf("cannot set read position %d beyond write position %d",
			position, sb.writePosition)
	}

	sb.readPosition = position
	return nil
}

// AdvanceReadPosition advances the read position by the specified number of bytes
func (sb *StreamBufferImpl) AdvanceReadPosition(bytes int) error {
	if bytes < 0 {
		return fmt.Errorf("cannot advance read position by negative bytes: %d", bytes)
	}

	sb.positionMutex.Lock()
	defer sb.positionMutex.Unlock()

	newPosition := sb.readPosition + uint64(bytes)
	if newPosition > sb.writePosition {
		return fmt.Errorf("cannot advance read position to %d beyond write position %d",
			newPosition, sb.writePosition)
	}

	sb.readPosition = newPosition
	return nil
}

// GetAvailableBytes returns the total number of bytes available for reading
func (sb *StreamBufferImpl) GetAvailableBytes() int {
	sb.positionMutex.RLock()
	defer sb.positionMutex.RUnlock()

	if sb.writePosition <= sb.readPosition {
		return 0
	}

	return int(sb.writePosition - sb.readPosition)
}

// GetContiguousBytes returns the number of contiguous bytes available from read position
func (sb *StreamBufferImpl) GetContiguousBytes() int {
	sb.positionMutex.RLock()
	currentReadPos := sb.readPosition
	sb.positionMutex.RUnlock()

	return len(sb.getContiguousDataFromPosition(currentReadPos, int(sb.totalSize)))
}

// HasGaps returns true if there are gaps in the data
func (sb *StreamBufferImpl) HasGaps() bool {
	sb.gapsMutex.RLock()
	defer sb.gapsMutex.RUnlock()
	return len(sb.gaps) > 0
}

// GetNextGapPosition returns the position of the next gap, if any
func (sb *StreamBufferImpl) GetNextGapPosition() (uint64, bool) {
	sb.gapsMutex.RLock()
	defer sb.gapsMutex.RUnlock()

	sb.positionMutex.RLock()
	currentReadPos := sb.readPosition
	sb.positionMutex.RUnlock()

	// Find the earliest gap at or after the current read position
	var earliestGap uint64 = ^uint64(0) // Max uint64
	found := false

	for gapStart := range sb.gaps {
		if gapStart >= currentReadPos && gapStart < earliestGap {
			earliestGap = gapStart
			found = true
		}
	}

	return earliestGap, found
}

// GetStreamMetadata returns the stream metadata
func (sb *StreamBufferImpl) GetStreamMetadata() *StreamMetadata {
	return sb.metadata
}

// UpdateStreamMetadata updates the stream metadata
func (sb *StreamBufferImpl) UpdateStreamMetadata(metadata *StreamMetadata) error {
	if metadata.StreamID != sb.streamID {
		return fmt.Errorf("cannot update metadata: stream ID mismatch (%d != %d)",
			metadata.StreamID, sb.streamID)
	}

	sb.metadata = metadata
	sb.metadata.LastActivity = time.Now()
	return nil
}

// GetBufferUsage returns current buffer usage statistics
func (sb *StreamBufferImpl) GetBufferUsage() *BufferUsage {
	sb.sizeMutex.RLock()
	usedSize := sb.usedSize
	totalSize := sb.totalSize
	sb.sizeMutex.RUnlock()

	sb.positionMutex.RLock()
	readPos := sb.readPosition
	writePos := sb.writePosition
	sb.positionMutex.RUnlock()

	// Get contiguous bytes from the beginning of data, not from current read position
	// This allows checking if data is available for re-reading
	contiguousBytes := uint64(len(sb.getContiguousDataFromPosition(0, int(totalSize))))

	sb.gapsMutex.RLock()
	gapCount := len(sb.gaps)
	var largestGapSize uint64 = 0
	for start, end := range sb.gaps {
		gapSize := end - start
		if gapSize > largestGapSize {
			largestGapSize = gapSize
		}
	}
	sb.gapsMutex.RUnlock()

	utilization := float64(usedSize) / float64(totalSize)

	return &BufferUsage{
		TotalCapacity:     totalSize,
		UsedCapacity:      usedSize,
		AvailableCapacity: totalSize - usedSize,
		ContiguousBytes:   contiguousBytes,
		GapCount:          gapCount,
		LargestGapSize:    largestGapSize,
		ReadPosition:      readPos,
		WritePosition:     writePos,
		Utilization:       utilization,
	}
}

// IsBackpressureNeeded returns true if backpressure should be applied
func (sb *StreamBufferImpl) IsBackpressureNeeded() bool {
	usage := sb.GetBufferUsage()
	threshold := DefaultStreamBackpressureThreshold
	if sb.config != nil {
		// We don't have a threshold in StreamBufferConfig, so we use the default
		// In a real implementation, this could be configurable
	}

	return usage.Utilization >= threshold
}

// Cleanup performs cleanup of old data and optimizes the buffer
func (sb *StreamBufferImpl) Cleanup() error {
	sb.positionMutex.RLock()
	readPos := sb.readPosition
	sb.positionMutex.RUnlock()

	// Clean up data that has been read
	sb.cleanupConsumedData(0, readPos)

	// Update buffer utilization history
	sb.updateBufferUtilizationHistory()

	return nil
}

// Close closes the stream buffer and releases resources
func (sb *StreamBufferImpl) Close() error {
	sb.closeMutex.Lock()
	defer sb.closeMutex.Unlock()

	if sb.closed {
		return nil // Already closed
	}

	sb.closed = true

	// Clear all data
	sb.chunksMutex.Lock()
	sb.dataChunks = make(map[uint64]*DataChunk)
	sb.chunksMutex.Unlock()

	sb.gapsMutex.Lock()
	sb.gaps = make(map[uint64]uint64)
	sb.gapsMutex.Unlock()

	sb.sizeMutex.Lock()
	sb.usedSize = 0
	sb.sizeMutex.Unlock()

	return nil
}

// SetBackpressureCallback sets the callback for backpressure notifications
func (sb *StreamBufferImpl) SetBackpressureCallback(callback func(streamID uint64, needed bool)) {
	sb.backpressureCallback = callback
}

// Helper methods

// getContiguousDataFromPosition returns contiguous data starting from the given position
func (sb *StreamBufferImpl) getContiguousDataFromPosition(startPos uint64, maxBytes int) []byte {
	sb.chunksMutex.RLock()
	defer sb.chunksMutex.RUnlock()

	var result []byte
	currentPos := startPos

	for len(result) < maxBytes {
		chunk, exists := sb.dataChunks[currentPos]
		if !exists {
			// Gap found, stop here
			break
		}

		// Calculate how much data we can take from this chunk
		remainingBytes := maxBytes - len(result)
		chunkData := chunk.Data

		if len(chunkData) <= remainingBytes {
			// Take the entire chunk
			result = append(result, chunkData...)
			currentPos += uint64(len(chunkData))
		} else {
			// Take partial chunk
			result = append(result, chunkData[:remainingBytes]...)
			break
		}
	}

	return result
}

// updateGapsAfterWrite updates the gap tracking after a write operation
func (sb *StreamBufferImpl) updateGapsAfterWrite(offset, length uint64) {
	sb.gapsMutex.Lock()
	defer sb.gapsMutex.Unlock()

	// Rebuild gaps from scratch by analyzing all chunks
	// This is simpler and more reliable than trying to incrementally update gaps
	sb.rebuildGapsLocked()
}

// rebuildGapsLocked rebuilds the gaps map from scratch based on current chunks
// Must be called with gapsMutex held
func (sb *StreamBufferImpl) rebuildGapsLocked() {
	// Clear existing gaps
	sb.gaps = make(map[uint64]uint64)

	// Get all chunk offsets and sort them
	sb.chunksMutex.RLock()
	var offsets []uint64
	chunkEnds := make(map[uint64]uint64) // offset -> end position
	for offset, chunk := range sb.dataChunks {
		offsets = append(offsets, offset)
		chunkEnds[offset] = offset + uint64(len(chunk.Data))
	}
	sb.chunksMutex.RUnlock()

	if len(offsets) == 0 {
		return // No data, no gaps
	}

	// Sort offsets
	sort.Slice(offsets, func(i, j int) bool {
		return offsets[i] < offsets[j]
	})

	// Find gaps between chunks
	for i := 0; i < len(offsets)-1; i++ {
		currentEnd := chunkEnds[offsets[i]]
		nextStart := offsets[i+1]

		if currentEnd < nextStart {
			// There's a gap between current chunk end and next chunk start
			sb.gaps[currentEnd] = nextStart
		}
	}

	// Check for gap at the beginning (before first chunk)
	if offsets[0] > 0 {
		sb.gaps[0] = offsets[0]
	}
}

// markDataAsConsumed marks data as consumed to free up buffer space for new writes
// but keeps the data available for re-reading
func (sb *StreamBufferImpl) markDataAsConsumed(startPos, length uint64) {
	endPos := startPos + length

	// Mark chunks as consumed but keep them for re-reading
	sb.chunksMutex.Lock()
	var consumedBytes uint64
	for offset, chunk := range sb.dataChunks {
		chunkEnd := offset + uint64(len(chunk.Data))
		if chunkEnd <= endPos {
			// This chunk has been completely consumed
			if chunk.Metadata == nil {
				chunk.Metadata = &DataMetadata{
					Properties: make(map[string]interface{}),
				}
			}
			if chunk.Metadata.Properties == nil {
				chunk.Metadata.Properties = make(map[string]interface{})
			}
			consumed, exists := chunk.Metadata.Properties["consumed"]
			isConsumed := exists && consumed == true
			if !isConsumed {
				chunk.Metadata.Properties["consumed"] = true
				consumedBytes += uint64(len(chunk.Data))
			}
		}
	}
	sb.chunksMutex.Unlock()

	// DON'T reduce usedSize here - this allows data to remain available for re-reading
	// The usedSize will be reduced only when data is actually cleaned up or overwritten

	// Debug logging
	if consumedBytes > 0 {
		// fmt.Printf("DEBUG: Stream %d markDataAsConsumed: startPos=%d, length=%d, consumedBytes=%d, usedSize=%d (unchanged)\n",
		// 	sb.streamID, startPos, length, consumedBytes, sb.usedSize)
	}

	// Trigger backpressure release if buffer was full
	if sb.backpressureCallback != nil && consumedBytes > 0 {
		sb.backpressureCallback(sb.streamID, false)
	}
}

// getConsumedSpaceSize returns the total size of consumed data that can be overwritten
func (sb *StreamBufferImpl) getConsumedSpaceSize() uint64 {
	sb.chunksMutex.RLock()
	defer sb.chunksMutex.RUnlock()

	var consumedSize uint64
	for _, chunk := range sb.dataChunks {
		if chunk.Metadata != nil && chunk.Metadata.Properties != nil {
			if consumed, exists := chunk.Metadata.Properties["consumed"]; exists && consumed == true {
				consumedSize += uint64(len(chunk.Data))
			}
		}
	}

	return consumedSize
}

// cleanupConsumedData removes data that has been consumed
func (sb *StreamBufferImpl) cleanupConsumedData(startPos, length uint64) {
	endPos := startPos + length

	sb.chunksMutex.Lock()
	var chunksToRemove []uint64
	var chunksToModify []struct {
		offset    uint64
		newData   []byte
		newOffset uint64
	}

	for offset, chunk := range sb.dataChunks {
		chunkEnd := offset + uint64(len(chunk.Data))

		if chunkEnd <= endPos {
			// This chunk has been completely consumed
			chunksToRemove = append(chunksToRemove, offset)
		} else if offset < endPos && chunkEnd > endPos {
			// This chunk is partially consumed - keep the unconsumed part
			consumedBytes := endPos - offset
			if consumedBytes > 0 && consumedBytes < uint64(len(chunk.Data)) {
				newData := make([]byte, len(chunk.Data)-int(consumedBytes))
				copy(newData, chunk.Data[consumedBytes:])
				chunksToModify = append(chunksToModify, struct {
					offset    uint64
					newData   []byte
					newOffset uint64
				}{
					offset:    offset,
					newData:   newData,
					newOffset: endPos,
				})
			}
		}
	}

	// Remove completely consumed chunks
	var freedBytes uint64
	for _, offset := range chunksToRemove {
		chunk := sb.dataChunks[offset]
		freedBytes += uint64(len(chunk.Data))
		delete(sb.dataChunks, offset)
	}

	// Modify partially consumed chunks
	for _, mod := range chunksToModify {
		oldChunk := sb.dataChunks[mod.offset]
		freedBytes += uint64(len(oldChunk.Data)) - uint64(len(mod.newData))

		// Remove old chunk and add new chunk at new offset
		delete(sb.dataChunks, mod.offset)
		sb.dataChunks[mod.newOffset] = &DataChunk{
			Offset:    mod.newOffset,
			Data:      mod.newData,
			Metadata:  oldChunk.Metadata, // Keep original metadata
			Timestamp: oldChunk.Timestamp,
		}
	}

	sb.chunksMutex.Unlock()

	// Update used size (prevent underflow)
	sb.sizeMutex.Lock()
	if sb.usedSize >= freedBytes {
		sb.usedSize -= freedBytes
	} else {
		sb.usedSize = 0
	}
	sb.sizeMutex.Unlock()

	// Remove gaps that have been consumed
	sb.gapsMutex.Lock()
	var gapsToRemove []uint64
	for gapStart, gapEnd := range sb.gaps {
		if gapEnd <= endPos {
			gapsToRemove = append(gapsToRemove, gapStart)
		} else if gapStart < endPos && gapEnd > endPos {
			// Partial gap consumption
			sb.gaps[endPos] = gapEnd
			gapsToRemove = append(gapsToRemove, gapStart)
		}
	}

	for _, gapStart := range gapsToRemove {
		delete(sb.gaps, gapStart)
	}
	sb.gapsMutex.Unlock()
}

// updateWriteStats updates write-related statistics
func (sb *StreamBufferImpl) updateWriteStats(bytesWritten int, latency time.Duration) {
	sb.statsMutex.Lock()
	defer sb.statsMutex.Unlock()

	sb.stats.BytesWritten += uint64(bytesWritten)
	sb.stats.WriteOperations++
	sb.stats.LastActivity = time.Now()

	// Update latency histogram
	sb.stats.WriteLatencyHistogram[latency]++

	// Update average latency (simple moving average)
	if sb.stats.WriteOperations == 1 {
		sb.stats.AverageLatency = latency
	} else {
		sb.stats.AverageLatency = time.Duration(
			(int64(sb.stats.AverageLatency)*int64(sb.stats.WriteOperations-1) + int64(latency)) /
				int64(sb.stats.WriteOperations))
	}
}

// updateReadStats updates read-related statistics
func (sb *StreamBufferImpl) updateReadStats(bytesRead int, latency time.Duration) {
	sb.statsMutex.Lock()
	defer sb.statsMutex.Unlock()

	sb.stats.BytesRead += uint64(bytesRead)
	sb.stats.ReadOperations++
	sb.stats.LastActivity = time.Now()

	// Update latency histogram
	sb.stats.ReadLatencyHistogram[latency]++
}

// updateBufferUtilizationHistory updates the buffer utilization history
func (sb *StreamBufferImpl) updateBufferUtilizationHistory() {
	usage := sb.GetBufferUsage()

	sb.statsMutex.Lock()
	defer sb.statsMutex.Unlock()

	// Add to history (keep last 100 measurements)
	sb.stats.BufferUtilizationHistory = append(sb.stats.BufferUtilizationHistory, usage.Utilization)
	if len(sb.stats.BufferUtilizationHistory) > 100 {
		sb.stats.BufferUtilizationHistory = sb.stats.BufferUtilizationHistory[1:]
	}

	// Update min/max
	if usage.Utilization > sb.stats.MaxBufferUtilization {
		sb.stats.MaxBufferUtilization = usage.Utilization
	}
	if usage.Utilization < sb.stats.MinBufferUtilization {
		sb.stats.MinBufferUtilization = usage.Utilization
	}
}

// GetGapInfo returns detailed information about gaps in the buffer
func (sb *StreamBufferImpl) GetGapInfo() []GapInfo {
	sb.gapsMutex.RLock()
	defer sb.gapsMutex.RUnlock()

	var gaps []GapInfo
	for start, end := range sb.gaps {
		gaps = append(gaps, GapInfo{
			StartOffset: start,
			EndOffset:   end,
			Size:        end - start,
			Duration:    time.Since(time.Now()), // This would need proper tracking in real implementation
		})
	}

	// Sort gaps by start offset
	sort.Slice(gaps, func(i, j int) bool {
		return gaps[i].StartOffset < gaps[j].StartOffset
	})

	return gaps
}

// WaitForContiguousData waits for contiguous data to become available up to the timeout
func (sb *StreamBufferImpl) WaitForContiguousData(minBytes int, timeout time.Duration) (int, error) {
	startTime := time.Now()

	for time.Since(startTime) < timeout {
		contiguousBytes := sb.GetContiguousBytes()
		if contiguousBytes >= minBytes {
			return contiguousBytes, nil
		}

		// Check if buffer is closed
		sb.closeMutex.RLock()
		if sb.closed {
			sb.closeMutex.RUnlock()
			return 0, fmt.Errorf("stream buffer %d is closed", sb.streamID)
		}
		sb.closeMutex.RUnlock()

		// Small sleep to avoid busy waiting
		time.Sleep(1 * time.Millisecond)
	}

	return sb.GetContiguousBytes(), fmt.Errorf("timeout waiting for %d contiguous bytes", minBytes)
}

// GetDataRange returns data in the specified range, including gaps
func (sb *StreamBufferImpl) GetDataRange(startOffset, endOffset uint64) ([]DataRangeChunk, error) {
	if startOffset >= endOffset {
		return nil, fmt.Errorf("invalid range: start %d >= end %d", startOffset, endOffset)
	}

	sb.chunksMutex.RLock()
	defer sb.chunksMutex.RUnlock()

	var result []DataRangeChunk
	currentOffset := startOffset

	for currentOffset < endOffset {
		if chunk, exists := sb.dataChunks[currentOffset]; exists {
			// Data chunk found
			chunkEnd := currentOffset + uint64(len(chunk.Data))
			if chunkEnd > endOffset {
				// Partial chunk
				partialData := make([]byte, endOffset-currentOffset)
				copy(partialData, chunk.Data[:endOffset-currentOffset])
				result = append(result, DataRangeChunk{
					StartOffset: currentOffset,
					EndOffset:   endOffset,
					Data:        partialData,
					IsGap:       false,
				})
				currentOffset = endOffset
			} else {
				// Full chunk
				result = append(result, DataRangeChunk{
					StartOffset: currentOffset,
					EndOffset:   chunkEnd,
					Data:        chunk.Data,
					IsGap:       false,
				})
				currentOffset = chunkEnd
			}
		} else {
			// Gap found - find the end of the gap
			gapEnd := currentOffset + 1
			for gapEnd < endOffset {
				if _, exists := sb.dataChunks[gapEnd]; exists {
					break
				}
				gapEnd++
			}

			result = append(result, DataRangeChunk{
				StartOffset: currentOffset,
				EndOffset:   gapEnd,
				Data:        nil,
				IsGap:       true,
			})
			currentOffset = gapEnd
		}
	}

	return result, nil
}

// FillGap attempts to fill a gap with the provided data
func (sb *StreamBufferImpl) FillGap(offset uint64, data []byte) error {
	// Check if this offset is actually a gap
	sb.gapsMutex.RLock()
	isGap := false
	for gapStart, gapEnd := range sb.gaps {
		if offset >= gapStart && offset < gapEnd {
			isGap = true
			break
		}
	}
	sb.gapsMutex.RUnlock()

	if !isGap {
		return fmt.Errorf("offset %d is not in a gap", offset)
	}

	// Use regular write to fill the gap
	return sb.Write(data, offset)
}

// GetContiguousRanges returns all contiguous data ranges in the buffer
func (sb *StreamBufferImpl) GetContiguousRanges() []ContiguousRange {
	sb.chunksMutex.RLock()
	defer sb.chunksMutex.RUnlock()

	if len(sb.dataChunks) == 0 {
		return nil
	}

	// Get all offsets and sort them
	var offsets []uint64
	for offset := range sb.dataChunks {
		offsets = append(offsets, offset)
	}
	sort.Slice(offsets, func(i, j int) bool {
		return offsets[i] < offsets[j]
	})

	var ranges []ContiguousRange
	currentStart := offsets[0]
	currentEnd := offsets[0] + uint64(len(sb.dataChunks[offsets[0]].Data))

	for i := 1; i < len(offsets); i++ {
		offset := offsets[i]
		chunkEnd := offset + uint64(len(sb.dataChunks[offset].Data))

		if offset == currentEnd {
			// Contiguous, extend current range
			currentEnd = chunkEnd
		} else {
			// Gap found, finalize current range and start new one
			ranges = append(ranges, ContiguousRange{
				StartOffset: currentStart,
				EndOffset:   currentEnd,
				Size:        currentEnd - currentStart,
			})
			currentStart = offset
			currentEnd = chunkEnd
		}
	}

	// Add the last range
	ranges = append(ranges, ContiguousRange{
		StartOffset: currentStart,
		EndOffset:   currentEnd,
		Size:        currentEnd - currentStart,
	})

	return ranges
}

// OptimizeBuffer performs buffer optimization by defragmenting and compacting data
func (sb *StreamBufferImpl) OptimizeBuffer() error {
	sb.chunksMutex.Lock()
	defer sb.chunksMutex.Unlock()

	// This is a simplified optimization - in a real implementation,
	// you might want to merge adjacent chunks, compress data, etc.

	// Update gap size histogram for statistics
	sb.statsMutex.Lock()
	sb.gapsMutex.RLock()
	for start, end := range sb.gaps {
		gapSize := end - start
		sb.stats.GapSizeHistogram[gapSize]++
	}
	sb.gapsMutex.RUnlock()
	sb.statsMutex.Unlock()

	return nil
}

// GetDetailedStats returns detailed statistics for this stream buffer
func (sb *StreamBufferImpl) GetDetailedStats() *DetailedStreamStats {
	sb.statsMutex.RLock()
	defer sb.statsMutex.RUnlock()

	// Create a copy to avoid race conditions
	stats := &DetailedStreamStats{
		StreamStats:              sb.stats.StreamStats,
		WriteLatencyHistogram:    make(map[time.Duration]uint64),
		ReadLatencyHistogram:     make(map[time.Duration]uint64),
		GapSizeHistogram:         make(map[uint64]uint64),
		BufferUtilizationHistory: make([]float64, len(sb.stats.BufferUtilizationHistory)),
		MaxBufferUtilization:     sb.stats.MaxBufferUtilization,
		MinBufferUtilization:     sb.stats.MinBufferUtilization,
		BackpressureDuration:     sb.stats.BackpressureDuration,
		BackpressureFrequency:    sb.stats.BackpressureFrequency,
		OutOfOrderPackets:        sb.stats.OutOfOrderPackets,
		DuplicatePackets:         sb.stats.DuplicatePackets,
		CorruptedPackets:         sb.stats.CorruptedPackets,
	}

	// Copy maps
	for k, v := range sb.stats.WriteLatencyHistogram {
		stats.WriteLatencyHistogram[k] = v
	}
	for k, v := range sb.stats.ReadLatencyHistogram {
		stats.ReadLatencyHistogram[k] = v
	}
	for k, v := range sb.stats.GapSizeHistogram {
		stats.GapSizeHistogram[k] = v
	}

	// Copy slice
	copy(stats.BufferUtilizationHistory, sb.stats.BufferUtilizationHistory)

	return stats
}

// Advanced cleanup and position management methods

// CleanupOldData removes data older than the specified duration
func (sb *StreamBufferImpl) CleanupOldData(maxAge time.Duration) error {
	cutoffTime := time.Now().Add(-maxAge)

	sb.chunksMutex.Lock()
	var chunksToRemove []uint64
	var freedBytes uint64

	for offset, chunk := range sb.dataChunks {
		if chunk.Timestamp.Before(cutoffTime) {
			// Only remove if it's before the current read position
			sb.positionMutex.RLock()
			readPos := sb.readPosition
			sb.positionMutex.RUnlock()

			chunkEnd := offset + uint64(len(chunk.Data))
			if chunkEnd <= readPos {
				chunksToRemove = append(chunksToRemove, offset)
				freedBytes += uint64(len(chunk.Data))
			}
		}
	}

	// Remove old chunks
	for _, offset := range chunksToRemove {
		delete(sb.dataChunks, offset)
	}
	sb.chunksMutex.Unlock()

	// Update used size (prevent underflow)
	sb.sizeMutex.Lock()
	if sb.usedSize >= freedBytes {
		sb.usedSize -= freedBytes
	} else {
		sb.usedSize = 0
	}
	sb.sizeMutex.Unlock()

	return nil
}

// ForceCleanupToPosition forcibly cleans up data up to the specified position
func (sb *StreamBufferImpl) ForceCleanupToPosition(position uint64) error {
	sb.cleanupConsumedData(0, position)

	// Update read position if it's behind
	sb.positionMutex.Lock()
	if sb.readPosition < position {
		sb.readPosition = position
	}
	sb.positionMutex.Unlock()

	return nil
}

// GetReadPositionInfo returns detailed information about the read position
func (sb *StreamBufferImpl) GetReadPositionInfo() *ReadPositionInfo {
	sb.positionMutex.RLock()
	readPos := sb.readPosition
	writePos := sb.writePosition
	sb.positionMutex.RUnlock()

	// Calculate bytes available for reading
	availableBytes := int64(writePos) - int64(readPos)
	if availableBytes < 0 {
		availableBytes = 0
	}

	// Find next data position
	sb.chunksMutex.RLock()
	var nextDataPos uint64 = writePos // Default to write position
	for offset := range sb.dataChunks {
		if offset >= readPos && offset < nextDataPos {
			nextDataPos = offset
		}
	}
	sb.chunksMutex.RUnlock()

	// Check if there's a gap at read position
	hasGapAtReadPos := false
	sb.gapsMutex.RLock()
	for gapStart, gapEnd := range sb.gaps {
		if readPos >= gapStart && readPos < gapEnd {
			hasGapAtReadPos = true
			break
		}
	}
	sb.gapsMutex.RUnlock()

	return &ReadPositionInfo{
		CurrentPosition:  readPos,
		WritePosition:    writePos,
		AvailableBytes:   uint64(availableBytes),
		NextDataPosition: nextDataPos,
		HasGapAtPosition: hasGapAtReadPos,
		ContiguousBytes:  uint64(sb.GetContiguousBytes()),
		LastUpdate:       time.Now(),
	}
}

// ResetReadPosition resets the read position to the beginning of available data
func (sb *StreamBufferImpl) ResetReadPosition() error {
	sb.chunksMutex.RLock()
	var earliestOffset uint64 = ^uint64(0) // Max uint64
	hasData := false

	for offset := range sb.dataChunks {
		if offset < earliestOffset {
			earliestOffset = offset
			hasData = true
		}
	}
	sb.chunksMutex.RUnlock()

	if !hasData {
		earliestOffset = 0
	}

	sb.positionMutex.Lock()
	sb.readPosition = earliestOffset
	sb.positionMutex.Unlock()

	return nil
}

// SkipToNextData skips the read position to the next available data
func (sb *StreamBufferImpl) SkipToNextData() (uint64, error) {
	sb.positionMutex.RLock()
	currentReadPos := sb.readPosition
	sb.positionMutex.RUnlock()

	sb.chunksMutex.RLock()
	var nextDataPos uint64 = ^uint64(0) // Max uint64
	hasNextData := false

	for offset := range sb.dataChunks {
		if offset > currentReadPos && offset < nextDataPos {
			nextDataPos = offset
			hasNextData = true
		}
	}
	sb.chunksMutex.RUnlock()

	if !hasNextData {
		return currentReadPos, fmt.Errorf("no data available after position %d", currentReadPos)
	}

	sb.positionMutex.Lock()
	sb.readPosition = nextDataPos
	sb.positionMutex.Unlock()

	// Update statistics for gap skip
	sb.statsMutex.Lock()
	sb.stats.GapEvents++
	sb.statsMutex.Unlock()

	return nextDataPos, nil
}

// GetCleanupStats returns statistics about cleanup operations
func (sb *StreamBufferImpl) GetCleanupStats() *CleanupStats {
	sb.chunksMutex.RLock()
	totalChunks := len(sb.dataChunks)
	sb.chunksMutex.RUnlock()

	sb.positionMutex.RLock()
	readPos := sb.readPosition
	writePos := sb.writePosition
	sb.positionMutex.RUnlock()

	sb.sizeMutex.RLock()
	usedSize := sb.usedSize
	totalSize := sb.totalSize
	sb.sizeMutex.RUnlock()

	// Calculate cleanable data (data before read position)
	sb.chunksMutex.RLock()
	var cleanableChunks int
	var cleanableBytes uint64

	for offset, chunk := range sb.dataChunks {
		if offset < readPos {
			// This chunk has some data that has been read and can be cleaned
			cleanableChunks++
			// Calculate how much of this chunk can be cleaned
			chunkEnd := offset + uint64(len(chunk.Data))
			if chunkEnd <= readPos {
				// Entire chunk can be cleaned
				cleanableBytes += uint64(len(chunk.Data))
			} else {
				// Only part of the chunk can be cleaned
				cleanableBytes += readPos - offset
			}
		}
	}
	sb.chunksMutex.RUnlock()

	return &CleanupStats{
		TotalChunks:        totalChunks,
		CleanableChunks:    cleanableChunks,
		CleanableBytes:     cleanableBytes,
		UsedBytes:          usedSize,
		TotalBytes:         totalSize,
		ReadPosition:       readPos,
		WritePosition:      writePos,
		FragmentationRatio: float64(totalChunks) / float64(writePos-readPos+1), // Simplified fragmentation metric
		LastCleanup:        time.Now(),                                         // This would be tracked properly in real implementation
	}
}

// PerformMaintenanceCleanup performs comprehensive maintenance cleanup
func (sb *StreamBufferImpl) PerformMaintenanceCleanup() error {
	// 1. Clean up consumed data
	if err := sb.Cleanup(); err != nil {
		return fmt.Errorf("failed to perform basic cleanup: %w", err)
	}

	// 2. Clean up old data (older than 1 minute)
	if err := sb.CleanupOldData(1 * time.Minute); err != nil {
		return fmt.Errorf("failed to cleanup old data: %w", err)
	}

	// 3. Optimize buffer
	if err := sb.OptimizeBuffer(); err != nil {
		return fmt.Errorf("failed to optimize buffer: %w", err)
	}

	// 4. Update utilization history
	sb.updateBufferUtilizationHistory()

	// 5. Check if backpressure can be released
	if sb.IsBackpressureNeeded() {
		if sb.backpressureCallback != nil {
			sb.backpressureCallback(sb.streamID, true)
		}
	} else {
		if sb.backpressureCallback != nil {
			sb.backpressureCallback(sb.streamID, false)
		}
	}

	return nil
}
