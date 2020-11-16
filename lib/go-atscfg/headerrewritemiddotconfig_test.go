package atscfg

/*
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

import (
	"strings"
	"testing"

	"github.com/apache/trafficcontrol/lib/go-tc"
	"github.com/apache/trafficcontrol/lib/go-util"
)

func TestMakeHeaderRewriteMidDotConfig(t *testing.T) {
	cdnName := "mycdn"
	hdr := "myHeaderComment"

	server := makeGenericServer()
	server.CDNName = &cdnName
	server.Cachegroup = util.StrPtr("edgeCG")
	server.HostName = util.StrPtr("myserver")
	serverStatus := string(tc.CacheStatusReported)
	server.Status = &serverStatus

	ds := makeGenericDS()
	ds.EdgeHeaderRewrite = util.StrPtr("edgerewrite")
	ds.ID = util.IntPtr(24)
	ds.XMLID = util.StrPtr("ds0")
	ds.MaxOriginConnections = util.IntPtr(42)
	ds.MidHeaderRewrite = util.StrPtr("midrewrite")
	ds.CDNName = &cdnName
	dsType := tc.DSTypeHTTP
	ds.Type = &dsType
	ds.ServiceCategory = util.StrPtr("servicecategory")

	mid0 := makeGenericServer()
	mid0.CDNName = &cdnName
	mid0.Cachegroup = util.StrPtr("midCG")
	mid0.HostName = util.StrPtr("mymid0")
	mid0Status := string(tc.CacheStatusReported)
	mid0.Status = &mid0Status

	mid1 := makeGenericServer()
	mid1.CDNName = &cdnName
	mid1.Cachegroup = util.StrPtr("midCG")
	mid1.HostName = util.StrPtr("mymid1")
	mid1Status := string(tc.CacheStatusOnline)
	mid1.Status = &mid1Status

	mid2 := makeGenericServer()
	mid2.CDNName = &cdnName
	mid2.Cachegroup = util.StrPtr("midCG")
	mid2.HostName = util.StrPtr("mymid2")
	mid2Status := string(tc.CacheStatusOffline)
	mid2.Status = &mid2Status

	eCG := &tc.CacheGroupNullable{}
	eCG.Name = server.Cachegroup
	eCG.ID = server.CachegroupID
	eCG.ParentName = mid0.Cachegroup
	eCG.ParentCachegroupID = mid0.CachegroupID
	eCGType := tc.CacheGroupEdgeTypeName
	eCG.Type = &eCGType

	mCG := &tc.CacheGroupNullable{}
	mCG.Name = mid0.Cachegroup
	mCG.ID = mid0.CachegroupID
	mCGType := tc.CacheGroupMidTypeName
	mCG.Type = &mCGType

	cgs := []tc.CacheGroupNullable{*eCG, *mCG}
	servers := []Server{*server, *mid0, *mid1, *mid2}
	dses := []tc.DeliveryServiceNullableV30{*ds}
	dss := makeDSS(servers, dses)

	fileName := "hdr_rw_mid_" + *ds.XMLID + ".config"

	cfg, err := MakeHeaderRewriteMidDotConfig(fileName, dses, dss, server, servers, cgs, hdr)
	if err != nil {
		t.Error(err)
	}

	txt := cfg.Text

	if !strings.Contains(txt, "midrewrite") {
		t.Errorf("expected no 'midrewrite' actual '%v'\n", txt)
	}

	if strings.Contains(txt, "edgerewrite") {
		t.Errorf("expected 'edgerewrite' actual '%v'\n", txt)
	}

	if !strings.Contains(txt, "origin_max_connections") {
		t.Errorf("expected origin_max_connections on edge header rewrite that uses the mids, actual '%v'\n", txt)
	}

	if !strings.Contains(txt, "21") { // 21, because max is 42, and there are 2 not-offline mids, so 42/2=21
		t.Errorf("expected origin_max_connections of 21, actual '%v'\n", txt)
	}
}

func TestMakeHeaderRewriteMidDotConfigNoMaxConns(t *testing.T) {
	cdnName := "mycdn"
	hdr := "myHeaderComment"

	server := makeGenericServer()
	server.CDNName = &cdnName
	server.Cachegroup = util.StrPtr("edgeCG")
	server.HostName = util.StrPtr("myserver")
	serverStatus := string(tc.CacheStatusReported)
	server.Status = &serverStatus

	ds := makeGenericDS()
	ds.EdgeHeaderRewrite = util.StrPtr("edgerewrite")
	ds.ID = util.IntPtr(24)
	ds.XMLID = util.StrPtr("ds0")
	ds.MidHeaderRewrite = util.StrPtr("midrewrite")
	ds.CDNName = &cdnName
	dsType := tc.DSTypeHTTP
	ds.Type = &dsType
	ds.ServiceCategory = util.StrPtr("servicecategory")

	mid0 := makeGenericServer()
	mid0.Cachegroup = util.StrPtr("midCG")
	mid0.HostName = util.StrPtr("mymid0")
	mid0Status := string(tc.CacheStatusReported)
	mid0.Status = &mid0Status

	mid1 := makeGenericServer()
	mid1.Cachegroup = util.StrPtr("midCG")
	mid1.HostName = util.StrPtr("mymid1")
	mid1Status := string(tc.CacheStatusOnline)
	mid1.Status = &mid1Status

	mid2 := makeGenericServer()
	mid2.Cachegroup = util.StrPtr("midCG")
	mid2.HostName = util.StrPtr("mymid2")
	mid2Status := string(tc.CacheStatusOffline)
	mid2.Status = &mid2Status

	eCG := &tc.CacheGroupNullable{}
	eCG.Name = server.Cachegroup
	eCG.ID = server.CachegroupID
	eCG.ParentName = mid0.Cachegroup
	eCG.ParentCachegroupID = mid0.CachegroupID
	eCGType := tc.CacheGroupEdgeTypeName
	eCG.Type = &eCGType

	mCG := &tc.CacheGroupNullable{}
	mCG.Name = mid0.Cachegroup
	mCG.ID = mid0.CachegroupID
	mCGType := tc.CacheGroupMidTypeName
	mCG.Type = &mCGType

	cgs := []tc.CacheGroupNullable{*eCG, *mCG}
	servers := []Server{*server, *mid0, *mid1, *mid2}
	dses := []tc.DeliveryServiceNullableV30{*ds}
	dss := makeDSS(servers, dses)

	// assignedMids := []HeaderRewriteServer{
	// 	HeaderRewriteServer{
	// 		Status: tc.CacheStatusReported,
	// 	},
	// 	HeaderRewriteServer{
	// 		Status: tc.CacheStatusOnline,
	// 	},
	// 	HeaderRewriteServer{
	// 		Status: tc.CacheStatusOffline,
	// 	},
	// }

	fileName := "hdr_rw_mid_" + *ds.XMLID + ".config"

	cfg, err := MakeHeaderRewriteMidDotConfig(fileName, dses, dss, mid0, servers, cgs, hdr)
	if err != nil {
		t.Error(err)
	}

	txt := cfg.Text

	if strings.Contains(txt, "origin_max_connections") {
		t.Errorf("expected no origin_max_connections on edge-only DS, actual '%v'\n", txt)
	}
}
