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

	"github.com/apache/trafficcontrol/lib/go-tc"
)

type CDNDS struct {
	OrgServerFQDN string
	QStringIgnore int
	CacheURL      string
	RegexRemap    string
}

func MakeRegexRemapDotConfig(
	cdnName tc.CDNName,
	toToolName string, // tm.toolname global parameter (TODO: cache itself?)
	toURL string, // tm.url global parameter (TODO: cache itself?)
	fileName string,
	dses map[tc.DeliveryServiceName]CDNDS,
) string {
	text := GenericHeaderComment(string(cdnName), toToolName, toURL)

	if len(fileName) < len(`regex_remap_.config`) {
		return text // TODO warn? Perl doesn't
	}

	dsName := fileName[len("regex_remap_") : len(fileName)-len(".config")]

	ds, ok := dses[tc.DeliveryServiceName(dsName)]
	if !ok {
		return text // TODO warn? Perl doesn't
	}

	text += ds.RegexRemap + "\n"
	text = strings.Replace(text, `__RETURN__`, "\n", -1)
	return text
}