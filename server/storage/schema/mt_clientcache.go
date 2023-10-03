// Copyright 2021 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package schema

import (
	"encoding/binary"
	"fmt"
	"github.com/golang/protobuf/proto"
	"go.etcd.io/etcd/server/v3/etcdserver/masterthesis"

	"go.etcd.io/etcd/server/v3/storage/backend"
)

func UnsafeCreateClientCacheBucket(tx backend.BatchTx) {
	tx.UnsafeCreateBucket(ClientCache)
}

func MustUnsafeGetAllCachedResponses(tx backend.ReadTx) []*masterthesis.CachedResponse {
	ls := make([]*masterthesis.CachedResponse, 0)
	err := tx.UnsafeForEach(ClientCache, func(k, v []byte) error {
		var cr masterthesis.CachedResponse
		err := proto.Unmarshal(v, &cr)
		if err != nil {
			return fmt.Errorf("failed to Unmarshal cached response proto item; client ID=%016x", bytesToClientID(k))
		}
		ls = append(ls, &cr)
		return nil
	})
	if err != nil {
		panic(err)
	}
	return ls
}

func MustUnsafePutCachedResponse(tx backend.BatchTx, cr *masterthesis.CachedResponse) {
	key := clientIDToBytes(cr.ClientID)

	val, err := proto.Marshal(cr)
	if err != nil {
		panic("failed to marshal cached response proto item")
	}
	tx.UnsafePut(ClientCache, key, val)
}

func UnsafeDeleteCachedResponse(tx backend.BatchTx, cr *masterthesis.CachedResponse) {
	tx.UnsafeDelete(ClientCache, clientIDToBytes(cr.ClientID))
}

func MustUnsafeGetCachedResponse(tx backend.BatchTx, clientID int64) *masterthesis.CachedResponse {
	_, vs := tx.UnsafeRange(ClientCache, clientIDToBytes(clientID), nil, 0)
	if len(vs) != 1 {
		return nil
	}
	var cr masterthesis.CachedResponse
	err := proto.Unmarshal(vs[0], &cr)
	if err != nil {
		panic("failed to unmarshal cached response proto item")
	}
	return &cr
}

func clientIDToBytes(n int64) []byte {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(n))
	return bytes
}

func bytesToClientID(bytes []byte) int64 {
	if len(bytes) != 8 {
		panic(fmt.Errorf("client ID must be 8-byte"))
	}
	return int64(binary.BigEndian.Uint64(bytes))
}
