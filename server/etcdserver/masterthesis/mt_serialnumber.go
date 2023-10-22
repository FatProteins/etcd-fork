package masterthesis

import (
	"errors"
	"github.com/gogo/protobuf/proto"
	"go.etcd.io/etcd/server/v3/storage/backend"
	"sync"
)

type SyncMap[K comparable, V any] struct {
	m sync.Map
}

func (m *SyncMap[K, V]) Delete(key K) { m.m.Delete(key) }
func (m *SyncMap[K, V]) Load(key K) (value V, ok bool) {
	v, ok := m.m.Load(key)
	if !ok {
		return value, ok
	}
	return v.(V), ok
}
func (m *SyncMap[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	v, loaded := m.m.LoadAndDelete(key)
	if !loaded {
		return value, loaded
	}
	return v.(V), loaded
}
func (m *SyncMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	a, loaded := m.m.LoadOrStore(key, value)
	return a.(V), loaded
}
func (m *SyncMap[K, V]) Range(f func(key K, value V) bool) {
	m.m.Range(func(key, value any) bool { return f(key.(K), value.(V)) })
}
func (m *SyncMap[K, V]) Store(key K, value V) { m.m.Store(key, value) }

type ClientCache struct {
	mutex    sync.RWMutex
	cacheMap map[int64]*CachedResponse
	be       backend.Backend
}

func NewClientCache(be backend.Backend) *ClientCache {
	clientCache := &ClientCache{
		cacheMap: make(map[int64]*CachedResponse),
		be:       be,
	}

	clientCache.initAndRecover()

	return clientCache
}

func (cc *ClientCache) initAndRecover() {
	tx := cc.be.BatchTx()

	tx.LockOutsideApply()
	UnsafeCreateClientCacheBucket(tx)
	crs := MustUnsafeGetAllCachedResponses(tx)
	tx.Unlock()
	for _, cr := range crs {
		ID := cr.ClientID
		cc.cacheMap[ID] = cr
	}

	cc.be.ForceCommit()
}

var ErrSerialNumberDeclined = errors.New("serial number was already acknowledged")
var ErrSerialNumberNotAppliedYet = errors.New("serial number was not yet applied to the state machine")

func (cc *ClientCache) GetCachedResponse(clientID, serialNumber int64) (proto.Message, error) {
	cc.mutex.RLock()
	defer cc.mutex.RUnlock()

	cr := cc.cacheMap[clientID]
	if cr.SerialNumber < serialNumber {
		return nil, nil
	}

	if cr.SerialNumber == serialNumber {
		if !cr.Applied {
			return nil, ErrSerialNumberNotAppliedYet
		}

		var msg proto.Message
		err := proto.Unmarshal(cr.Response, msg)
		if err != nil {
			return nil, err
		}

		return msg, nil
	}

	return nil, ErrSerialNumberDeclined
}

func (cc *ClientCache) StartRequest(tx backend.BatchTx, clientID, serialNumber int64, logIdx uint64) *CachedResponse {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	cr := cc.cacheMap[clientID]
	if cr != nil {
		if cr.SerialNumber >= serialNumber {
			panic("attempted to start new client request with serial number lower than acknowledged")
		}
	} else {
		cr = &CachedResponse{ClientID: clientID}
	}

	cr.SerialNumber = serialNumber
	cr.LogIdx = logIdx
	tx.LockOutsideApply()
	MustUnsafePutCachedResponse(tx, cr)
	tx.Unlock()
	cc.cacheMap[clientID] = cr

	return cr
}

func (cc *ClientCache) UnsafeStoreResponseBeforeApply(tx backend.BatchTx, clientID, serialNumber int64, response proto.Message) {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	cr := cc.cacheMap[clientID]
	if cr == nil {
		panic("cached request not started before apply")
	}

	if cr.SerialNumber != serialNumber {
		panic("serial number in cached response does not match serial number on apply")
	}

	data, err := proto.Marshal(response)
	if err != nil {
		panic(err)
	}

	cr.Response = data
	MustUnsafePutCachedResponse(tx, cr)
	cc.cacheMap[clientID] = cr
}

func (cc *ClientCache) UnsafeSetApplied(tx backend.BatchTx, clientID, serialNumber int64) {
	cc.mutex.Lock()
	defer cc.mutex.Unlock()

	cr := cc.cacheMap[clientID]
	if cr == nil {
		panic("cached request not started before setting applied")
	}

	if cr.SerialNumber != serialNumber {
		panic("serial number in cached response does not match serial number after apply")
	}

	cr.Applied = true
	MustUnsafePutCachedResponse(tx, cr)
	cc.cacheMap[clientID] = cr
}
