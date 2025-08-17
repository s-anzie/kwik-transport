package presentation

import (
	"sync"
	"time"
)

// MemoryPool manages reusable memory blocks to reduce allocation overhead
type MemoryPool struct {
	pools map[int]*sync.Pool // Size -> Pool mapping
	mutex sync.RWMutex
	
	// Statistics
	allocations   int64
	deallocations int64
	hits          int64
	misses        int64
	
	// Configuration
	maxBlockSize    int
	cleanupInterval time.Duration
	
	// Cleanup management
	stopCleanup chan struct{}
	cleanupWg   sync.WaitGroup
}

// MemoryBlock represents a reusable memory block
type MemoryBlock struct {
	data     []byte
	size     int
	capacity int
	inUse    bool
}

// NewMemoryPool creates a new memory pool with the specified configuration
func NewMemoryPool(maxBlockSize int, cleanupInterval time.Duration) *MemoryPool {
	mp := &MemoryPool{
		pools:           make(map[int]*sync.Pool),
		maxBlockSize:    maxBlockSize,
		cleanupInterval: cleanupInterval,
		stopCleanup:     make(chan struct{}),
	}
	
	// Initialize common block sizes
	commonSizes := []int{64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}
	for _, size := range commonSizes {
		if size <= maxBlockSize {
			mp.initializePool(size)
		}
	}
	
	// Start cleanup routine
	mp.cleanupWg.Add(1)
	go mp.cleanupRoutine()
	
	return mp
}

// initializePool creates a sync.Pool for a specific block size
func (mp *MemoryPool) initializePool(size int) {
	mp.pools[size] = &sync.Pool{
		New: func() interface{} {
			return &MemoryBlock{
				data:     make([]byte, size),
				size:     size,
				capacity: size,
				inUse:    false,
			}
		},
	}
}

// GetBlock retrieves a memory block of at least the specified size
func (mp *MemoryPool) GetBlock(size int) *MemoryBlock {
	if size > mp.maxBlockSize {
		// For very large blocks, allocate directly
		mp.misses++
		return &MemoryBlock{
			data:     make([]byte, size),
			size:     size,
			capacity: size,
			inUse:    true,
		}
	}
	
	// Find the smallest pool that can accommodate the request
	poolSize := mp.findPoolSize(size)
	
	mp.mutex.RLock()
	pool, exists := mp.pools[poolSize]
	mp.mutex.RUnlock()
	
	if !exists {
		mp.mutex.Lock()
		// Double-check after acquiring write lock
		if pool, exists = mp.pools[poolSize]; !exists {
			mp.initializePool(poolSize)
			pool = mp.pools[poolSize]
		}
		mp.mutex.Unlock()
	}
	
	block := pool.Get().(*MemoryBlock)
	block.inUse = true
	block.size = size // Actual requested size
	
	mp.allocations++
	mp.hits++
	
	return block
}

// PutBlock returns a memory block to the pool
func (mp *MemoryPool) PutBlock(block *MemoryBlock) {
	if block == nil || !block.inUse {
		return
	}
	
	block.inUse = false
	
	// Don't pool very large blocks
	if block.capacity > mp.maxBlockSize {
		mp.deallocations++
		return
	}
	
	// Reset the block
	block.size = block.capacity
	
	mp.mutex.RLock()
	pool, exists := mp.pools[block.capacity]
	mp.mutex.RUnlock()
	
	if exists {
		pool.Put(block)
		mp.deallocations++
	}
}

// findPoolSize finds the smallest pool size that can accommodate the request
func (mp *MemoryPool) findPoolSize(size int) int {
	// Round up to the next power of 2 or common size
	if size <= 64 {
		return 64
	}
	if size <= 128 {
		return 128
	}
	if size <= 256 {
		return 256
	}
	if size <= 512 {
		return 512
	}
	if size <= 1024 {
		return 1024
	}
	if size <= 2048 {
		return 2048
	}
	if size <= 4096 {
		return 4096
	}
	if size <= 8192 {
		return 8192
	}
	if size <= 16384 {
		return 16384
	}
	if size <= 32768 {
		return 32768
	}
	if size <= 65536 {
		return 65536
	}
	
	// For larger sizes, round up to next multiple of 64KB
	return ((size + 65535) / 65536) * 65536
}

// GetStats returns memory pool statistics
func (mp *MemoryPool) GetStats() MemoryPoolStats {
	mp.mutex.RLock()
	defer mp.mutex.RUnlock()
	
	totalBlocks := 0
	usedBlocks := int(mp.allocations - mp.deallocations)
	
	// Estimate total blocks based on active pools
	for range mp.pools {
		// This is an approximation since sync.Pool doesn't expose exact counts
		totalBlocks += 10 // Assume average of 10 blocks per pool
	}
	
	availableBlocks := totalBlocks - usedBlocks
	if availableBlocks < 0 {
		availableBlocks = 0
	}
	
	utilization := 0.0
	if totalBlocks > 0 {
		utilization = float64(usedBlocks) / float64(totalBlocks)
	}
	
	return MemoryPoolStats{
		TotalBlocks:     totalBlocks,
		UsedBlocks:      usedBlocks,
		AvailableBlocks: availableBlocks,
		Utilization:     utilization,
		TotalAllocated:  uint64(mp.allocations),
		TotalReleased:   uint64(mp.deallocations),
		LastUpdate:      time.Now(),
	}
}

// cleanupRoutine performs periodic cleanup of unused memory blocks
func (mp *MemoryPool) cleanupRoutine() {
	defer mp.cleanupWg.Done()
	
	ticker := time.NewTicker(mp.cleanupInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			mp.performCleanup()
		case <-mp.stopCleanup:
			return
		}
	}
}

// performCleanup removes unused blocks from pools to free memory
func (mp *MemoryPool) performCleanup() {
	mp.mutex.Lock()
	defer mp.mutex.Unlock()
	
	// For each pool, we could implement more sophisticated cleanup
	// For now, we rely on Go's GC to handle unused blocks in sync.Pool
	// In a production system, you might want to implement custom cleanup logic
}

// Shutdown stops the memory pool and cleanup routines
func (mp *MemoryPool) Shutdown() {
	close(mp.stopCleanup)
	mp.cleanupWg.Wait()
}

// MemoryPoolStatsInternal contains internal statistics about memory pool usage
type MemoryPoolStatsInternal struct {
	Allocations   int64
	Deallocations int64
	Hits          int64
	Misses        int64
	HitRate       float64
	ActivePools   int
}

// GlobalMemoryManager is a singleton memory pool manager
type GlobalMemoryManager struct {
	pool *MemoryPool
	once sync.Once
}

var globalMemoryManager *GlobalMemoryManager

// GetGlobalMemoryManager returns the singleton memory manager
func GetGlobalMemoryManager() *GlobalMemoryManager {
	if globalMemoryManager == nil {
		globalMemoryManager = &GlobalMemoryManager{}
		globalMemoryManager.once.Do(func() {
			globalMemoryManager.pool = NewMemoryPool(1024*1024, 30*time.Second) // 1MB max, cleanup every 30s
		})
	}
	return globalMemoryManager
}

// GetBlock retrieves a memory block from the global pool
func (gmm *GlobalMemoryManager) GetBlock(size int) *MemoryBlock {
	return gmm.pool.GetBlock(size)
}

// PutBlock returns a memory block to the global pool
func (gmm *GlobalMemoryManager) PutBlock(block *MemoryBlock) {
	gmm.pool.PutBlock(block)
}

// GetStats returns global memory pool statistics
func (gmm *GlobalMemoryManager) GetStats() MemoryPoolStats {
	return gmm.pool.GetStats()
}

// Shutdown shuts down the global memory manager
func (gmm *GlobalMemoryManager) Shutdown() {
	if gmm.pool != nil {
		gmm.pool.Shutdown()
	}
}

// OptimizedBuffer is a buffer that uses the memory pool for efficient allocation
type OptimizedBuffer struct {
	block  *MemoryBlock
	offset int
	length int
}

// NewOptimizedBuffer creates a new optimized buffer with the specified size
func NewOptimizedBuffer(size int) *OptimizedBuffer {
	block := GetGlobalMemoryManager().GetBlock(size)
	return &OptimizedBuffer{
		block:  block,
		offset: 0,
		length: size,
	}
}

// Data returns the underlying data slice
func (ob *OptimizedBuffer) Data() []byte {
	if ob.block == nil {
		return nil
	}
	return ob.block.data[ob.offset : ob.offset+ob.length]
}

// Write writes data to the buffer at the specified offset
func (ob *OptimizedBuffer) Write(data []byte, offset int) error {
	if ob.block == nil {
		return ErrBufferClosed
	}
	
	if offset+len(data) > ob.length {
		return ErrBufferOverflow
	}
	
	copy(ob.block.data[ob.offset+offset:], data)
	return nil
}

// Read reads data from the buffer at the specified offset
func (ob *OptimizedBuffer) Read(offset int, length int) ([]byte, error) {
	if ob.block == nil {
		return nil, ErrBufferClosed
	}
	
	if offset+length > ob.length {
		return nil, ErrBufferOverflow
	}
	
	result := make([]byte, length)
	copy(result, ob.block.data[ob.offset+offset:ob.offset+offset+length])
	return result, nil
}

// Close releases the buffer back to the memory pool
func (ob *OptimizedBuffer) Close() error {
	if ob.block != nil {
		GetGlobalMemoryManager().PutBlock(ob.block)
		ob.block = nil
	}
	return nil
}

// Size returns the buffer size
func (ob *OptimizedBuffer) Size() int {
	return ob.length
}

// Capacity returns the underlying block capacity
func (ob *OptimizedBuffer) Capacity() int {
	if ob.block == nil {
		return 0
	}
	return ob.block.capacity
}