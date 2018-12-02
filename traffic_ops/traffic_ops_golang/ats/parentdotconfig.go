package ats

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
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/apache/trafficcontrol/lib/go-log"
	"github.com/apache/trafficcontrol/lib/go-tc"
	"github.com/apache/trafficcontrol/traffic_ops/traffic_ops_golang/api"

	"github.com/lib/pq"
)

const TypeCacheGroupOrigin = "ORG_LOC"
const DefaultATSVersion = "5" // emulates Perl

func GetParentDotConfig(w http.ResponseWriter, r *http.Request) {
	inf, userErr, sysErr, errCode := api.NewInfo(r, []string{"id-or-host"}, nil)
	if userErr != nil || sysErr != nil {
		api.HandleErr(w, r, inf.Tx.Tx, errCode, userErr, sysErr)
		return
	}
	defer inf.Close()

	idOrHost := strings.TrimSuffix(inf.Params["id-or-host"], ".json")
	hostName := ""
	isHost := false
	id, err := strconv.Atoi(idOrHost)
	if err != nil {
		isHost = true
		hostName = idOrHost
	}

	serverInfo, ok, err := &ServerInfo{}, false, error(nil)
	if isHost {
		serverInfo, ok, err = getServerInfoByHost(inf.Tx.Tx, hostName)
	} else {
		serverInfo, ok, err = getServerInfoByID(inf.Tx.Tx, id)
	}
	if err != nil {
		api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting server info: "+err.Error()))
		return
	}
	if !ok {
		api.HandleErr(w, r, inf.Tx.Tx, http.StatusNotFound, errors.New("server not found"), nil)
		return
	}

	atsMajorVer, err := GetATSMajorVersion(inf.Tx.Tx, serverInfo.ProfileID)
	if err != nil {
		api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting ATS major version: "+err.Error()))
		return
	}

	hdr, err := headerComment(inf.Tx.Tx, serverInfo.HostName)
	if err != nil {
		api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting header comment: "+err.Error()))
		return
	}

	textArr := []string{}
	text := ""
	// TODO put these in separate functions. No if-statement should be this long.
	if serverInfo.IsTopLevelCache() {
		log.Errorf("DEBUG PCGen isTopLevel\n")
		uniqueOrigins := map[string]struct{}{}

		data, err := getParentConfigDSTopLevel(inf.Tx.Tx, serverInfo.CDN)
		if err != nil {
			api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting parent config DS data: "+err.Error()))
			return
		}

		parentInfos := map[string][]ParentInfo{} // TODO better names (this was transliterated from Perl)

		for _, ds := range data {
			parentQStr := "ignore"
			if ds.QStringHandling == "" && ds.MSOAlgorithm == AlgorithmConsistentHash && ds.QStringIgnore == 0 {
				parentQStr = "consider"
			}

			orgURIStr := ds.OriginFQDN
			orgURI, err := url.Parse(orgURIStr) // TODO verify origin is always a host:port
			if err != nil {
				log.Errorln("Malformed ds '" + string(ds.Name) + "' origin  URI: '" + orgURIStr + "', skipping! : " + err.Error())
				continue
			}
			// TODO put in function, to remove duplication
			if orgURI.Port() == "" {
				if orgURI.Scheme == "http" {
					orgURI.Host += ":80"
				} else if orgURI.Scheme == "https" {
					orgURI.Host += ":443"
				} else {
					log.Errorln("parent.config generation: fds '" + string(ds.Name) + "' origin  URI: '" + orgURIStr + "' is unknown scheme '" + orgURI.Scheme + "', but has no port! Using as-is! ")
				}
			}

			if _, ok := uniqueOrigins[ds.OriginFQDN]; ok {
				continue // TODO warn?
			}
			uniqueOrigins[ds.OriginFQDN] = struct{}{}

			if ds.OriginShield != "" {
				serverParams, err := getParentConfigServerProfileParams(inf.Tx.Tx, serverInfo.ID)
				if err != nil {
					api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting server params: "+err.Error()))
					return
				}

				algorithm := ""
				if parentSelectAlg, hasParentSelectAlg := serverParams[ParentConfigParamAlgorithm]; hasParentSelectAlg {
					algorithm = "round_robin=" + parentSelectAlg
				}
				text += "dest_domain=" + orgURI.Hostname() + " port=" + orgURI.Port() + " parent=" + ds.OriginShield + " " + algorithm + " go_direct=true\n"
			} else if ds.MultiSiteOrigin {
				text += "dest_domain=" + orgURI.Hostname() + " port=" + orgURI.Port() + " "
				if len(parentInfos) == 0 {
					// If we have multi-site origin, get parent_data once
					parentInfos, err = getParentInfo(inf.Tx.Tx, serverInfo)
					if err != nil {
						api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting server parent info: "+err.Error()))
						return
					}

					if len(parentInfos[orgURI.Host]) == 0 {
						// TODO error? emulates Perl
						log.Warnln("ParentInfo: delivery service " + ds.Name + " has no parent servers")
					}
					rankedParents := ParentInfoSortByRank(parentInfos[orgURI.Host])
					sort.Sort(rankedParents)

					parentInfo := []string{}
					secondaryParentInfo := []string{}
					nullParentInfo := []string{}
					for _, parent := range ([]ParentInfo)(rankedParents) {
						if parent.PrimaryParent {
							parentInfo = append(parentInfo, parent.Format())
						} else if parent.SecondaryParent {
							secondaryParentInfo = append(secondaryParentInfo, parent.Format())
						} else {
							nullParentInfo = append(nullParentInfo, parent.Format())
						}
					}

					if len(parentInfo) == 0 {
						// If no parents are found in the secondary parent either, then set the null parent list (parents in neither secondary or primary)
						// as the secondary parent list and clear the null parent list.
						if len(secondaryParentInfo) == 0 {
							secondaryParentInfo = nullParentInfo
							nullParentInfo = []string{}
						}
						parentInfo = secondaryParentInfo
						secondaryParentInfo = []string{} // TODO should thi be '= secondary'? Currently emulates Perl
					}

					// TODO benchmark, verify this isn't slow. if it is, it could easily be made faster
					seen := map[string]struct{}{} // TODO change to host+port? host isn't unique
					parentInfo, seen = removeStrDuplicates(parentInfo, seen)
					secondaryParentInfo, seen = removeStrDuplicates(secondaryParentInfo, seen)
					nullParentInfo, seen = removeStrDuplicates(nullParentInfo, seen)

					// If the ats version supports it and the algorithm is consistent hash, put secondary and non-primary parents into secondary parent group.
					// This will ensure that secondary and tertiary parents will be unused unless all hosts in the primary group are unavailable.

					parents := ""

					if atsMajorVer >= 6 && ds.MSOAlgorithm == "consistent_hash" && (len(secondaryParentInfo) > 0 || len(nullParentInfo) > 0) {
						parents = `parent="` + strings.Join(parentInfo, "") + `" secondary_parent="` + strings.Join(secondaryParentInfo, "") + strings.Join(nullParentInfo, "") + `"`
					} else {
						parents = `parent="` + strings.Join(parentInfo, "") + strings.Join(secondaryParentInfo, "") + strings.Join(nullParentInfo, "") + `"`
					}
					text += parents + ` round_robin=` + ds.MSOAlgorithm + ` qstring=` + parentQStr + ` go_direct=false parent_is_proxy=false`

					parentRetry := ds.MSOParentRetry
					if atsMajorVer >= 6 && parentRetry != "" {
						removeSpaceReplacer := strings.NewReplacer(" ", "", "\t", "", "\n", "")
						unavailableServerRetryResponses := removeSpaceReplacer.Replace(ds.MSOUnavailableServerRetryResponses)
						if unavailableServerRetryResponsesValid(unavailableServerRetryResponses) {
							text += ` parent_retry=` + parentRetry + ` unavailable_server_retry_responses=` + unavailableServerRetryResponses
						} else {
							if unavailableServerRetryResponses != "" {
								log.Errorln("Malformed unavailable_server_retry_responses parameter '" + unavailableServerRetryResponses + "', not using!")
							}
							text += ` parent_retry=` + parentRetry
						}
						text += ` max_simple_retries=` + ds.MSOMaxSimpleRetries + ` max_unavailable_server_retries=` + ds.MSOMaxUnavailableServerRetries
					}
					text += "\n"
					textArr = append(textArr, text)
				}
			}
		}

		sort.Sort(sort.StringSlice(textArr))
		text = hdr + strings.Join(textArr, "")
	} else {
		log.Errorf("DEBUG PCGen not top level\n")
		// not a top level cache
		data, err := getParentConfigDS(inf.Tx.Tx, serverInfo.ID) // TODO rename
		if err != nil {
			api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting parent config DS data (non-top-level): "+err.Error()))
			return
		}

		log.Errorf("DEBUG PCGen got data len %+v\n", len(data))

		parentInfos, err := getParentInfo(inf.Tx.Tx, serverInfo)
		if err != nil {
			api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting server parent info (non-top-level: "+err.Error()))
			return
		}

		log.Errorf("DEBUG PCGen got parent info len %+v\n", len(parentInfos))

		done := map[string]tc.DeliveryServiceName{}                                       // map[originHost]ds
		serverParams, err := getParentConfigServerProfileParams(inf.Tx.Tx, serverInfo.ID) // (map[string]string, error) {
		if err != nil {
			api.HandleErr(w, r, inf.Tx.Tx, http.StatusInternalServerError, nil, errors.New("Getting parent config server profile params: "+err.Error()))
			return
		}

		log.Errorf("DEBUG PCGen got server params len %+v\n", len(serverParams))

		qsh := serverParams[ParentConfigParamQStringHandling] // TODO rename
		parentInfo := []string{}
		secondaryParentInfo := []string{}

		log.Errorf("DEBUG PCGen ranging over len(parentInfos[DeliveryServicesAllParentsKey]) %+v\n", len(parentInfos[DeliveryServicesAllParentsKey]))

		for _, parent := range parentInfos[DeliveryServicesAllParentsKey] { // TODO fix magic key
			pTxt := parent.Format()
			if parent.PrimaryParent {
				parentInfo = append(parentInfo, pTxt)
			} else if parent.SecondaryParent {
				secondaryParentInfo = append(secondaryParentInfo, pTxt)
			}
		}

		if len(parentInfo) == 0 {
			parentInfo = secondaryParentInfo
			secondaryParentInfo = []string{}
		}

		// TODO remove duplicate code with top level if block
		seen := map[string]struct{}{} // TODO change to host+port? host isn't unique
		parentInfo, seen = removeStrDuplicates(parentInfo, seen)
		secondaryParentInfo, seen = removeStrDuplicates(secondaryParentInfo, seen)

		parents := ""
		secParents := "" // TODO rename
		sort.Sort(sort.StringSlice(parentInfo))
		sort.Sort(sort.StringSlice(secondaryParentInfo))
		if atsMajorVer >= 6 && len(secondaryParentInfo) > 0 {
			parents = `parent="` + strings.Join(parentInfo, "") + `"`
			secParents = ` secondary_parent="` + strings.Join(secondaryParentInfo, "") + `"`
		} else {
			parents = `parent="` + strings.Join(parentInfo, "") + strings.Join(secondaryParentInfo, "") + `"`
		}

		roundRobin := `round_robin=consistent_hash`
		goDirect := `go_direct=false` // TODO double-check with Perl, that this isn't ever different

		sort.Sort(ParentConfigDSSortByName(data))
		for _, ds := range data {
			text := ""
			originFQDN := ds.OriginFQDN
			if originFQDN == "" {
				continue // TODO warn? (Perl doesn't)
			}

			orgURI, err := url.Parse(originFQDN) // TODO verify
			if err != nil {
				log.Errorln("Malformed ds '" + string(ds.Name) + "' origin  URI: '" + originFQDN + "': skipping!" + err.Error())
				continue
			}

			if existingDS, ok := done[originFQDN]; ok {
				log.Errorln("parent.config generation: duplicate origin! services '" + string(ds.Name) + "' and '" + string(existingDS) + "' share origin '" + orgURI.Host + "': skipping '" + string(ds.Name) + "'!")
				continue
			}

			// TODO put in function, to remove duplication
			if orgURI.Port() == "" {
				if orgURI.Scheme == "http" {
					orgURI.Host += ":80"
				} else if orgURI.Scheme == "https" {
					orgURI.Host += ":443"
				} else {
					log.Errorln("parent.config generation non-top-level: ds '" + string(ds.Name) + "' origin  URI: '" + originFQDN + "' is unknown scheme '" + orgURI.Scheme + "', but has no port! Using as-is! ")
				}
			}

			// debug
			if orgURI.Hostname() == "odol-cim-linear-hds.cmc.co.ndcwest.comcast.net" {
				log.Errorf("DEBUG ds '%+v' orgURI '%+v' is %++v\n", ds.Name, orgURI.Hostname(), ds)
			}
			// TODO encode this in a DSType func, IsGoDirect() ?
			if dsType := tc.DSType(ds.Type); dsType == tc.DSTypeHTTPNoCache || dsType == tc.DSTypeHTTPLive || dsType == tc.DSTypeDNSLive {
				text += `dest_domain=` + orgURI.Hostname() + ` port=` + orgURI.Port() + ` go_direct=true` + "\n"
			} else {

				// check for profile psel.qstring_handling.  If this parameter is assigned to the server profile,
				// then edges will use the qstring handling value specified in the parameter for all profiles.

				// If there is no defined parameter in the profile, then check the delivery service profile.
				// If psel.qstring_handling exists in the DS profile, then we use that value for the specified DS only.
				// This is used only if not overridden by a server profile qstring handling parameter.

				// TODO refactor this logic, hard to understand (transliterated from Perl)
				dsQSH := qsh
				if dsQSH == "" {
					dsQSH = ds.QStringHandling
				}
				parentQStr := dsQSH
				if parentQStr == "" {
					parentQStr = "ignore"
				}
				if ds.QStringIgnore == 0 && dsQSH == "" {
					parentQStr = "consider"
				}

				text += `dest_domain=` + orgURI.Hostname() + ` port=` + orgURI.Port() + ` ` + parents + ` ` + secParents + ` ` + roundRobin + ` ` + goDirect + ` qstring=` + parentQStr + "\n"
			}
			textArr = append(textArr, text)
			done[originFQDN] = ds.Name
		}

		defaultDestText := ""
		if serverParams[ParentConfigParamAlgorithm] == AlgorithmConsistentHash {
			defaultDestText += `dest_domain=. ` + parents + secParents + ` round_robin=consistent_hash go_direct=false`
		} else {
			// default to old situation.
			// TODO fix - urlhash is not a valid ATS config - see https://github.com/apache/trafficcontrol/issues/3071
			defaultDestText += `dest_domain=. ` + parents + ` round_robin=urlhash go_direct=false`
		}

		if qStr := serverParams[ParentConfigParamQString]; qStr != "" {
			defaultDestText += ` qstring=` + qStr
		}
		defaultDestText += "\n"

		sort.Sort(sort.StringSlice(textArr))
		text = hdr + strings.Join(textArr, "") + defaultDestText
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(text))
}

// unavailableServerRetryResponsesValid returns whether a unavailable_server_retry_responses parameter is valid for an ATS parent rule.
func unavailableServerRetryResponsesValid(s string) bool {
	// optimization if param is empty
	if s == "" {
		return false
	}
	re := regexp.MustCompile(`^(:?\d{3},)+\d{3}$`) // TODO benchmark, cache if performance matters
	return re.MatchString(s)
}
func removeStrDuplicates(pi []string, seen map[string]struct{}) ([]string, map[string]struct{}) {
	npi := []string{}
	for _, p := range pi {
		if _, ok := seen[p]; !ok {
			npi = append(npi, p)
			seen[p] = struct{}{}
		}
	}
	return npi, seen
}

type OriginHost string

type ParentInfos map[OriginHost]ParentInfo

func (p ParentInfo) Format() string {
	host := ""
	if p.UseIP {
		host = p.IP
	} else {
		host = p.Host + "." + p.Domain
	}
	return host + ":" + strconv.Itoa(p.Port) + "|" + strconv.FormatFloat(p.Weight, 'f', -1, 64) + ";"
}

type ParentInfoSortByRank []ParentInfo

func (s ParentInfoSortByRank) Len() int           { return len(([]ParentInfo)(s)) }
func (s ParentInfoSortByRank) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s ParentInfoSortByRank) Less(i, j int) bool { return s[i].Rank < s[j].Rank }

type ParentConfigDSSortByName []ParentConfigDS

func (s ParentConfigDSSortByName) Len() int      { return len(([]ParentConfigDS)(s)) }
func (s ParentConfigDSSortByName) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s ParentConfigDSSortByName) Less(i, j int) bool {
	// TODO make this match the Perl sort "foreach my $ds ( sort @{ $data->{dslist} } )" ?
	return strings.Compare(string(s[i].Name), string(s[j].Name)) < 0
}

//ParentConfigDS

const AlgorithmConsistentHash = "consistent_hash"

type ServerInfo struct {
	CacheGroupID                  int
	CDN                           tc.CDNName
	CDNID                         int
	DomainName                    string
	HostName                      string
	ID                            int
	IP                            string
	ParentCacheGroupID            int
	ParentCacheGroupType          string
	ProfileID                     ProfileID
	ProfileName                   string
	Port                          int
	SecondaryParentCacheGroupID   int
	SecondaryParentCacheGroupType string
	Type                          string
}

func (s *ServerInfo) IsTopLevelCache() bool {
	return (s.ParentCacheGroupType == TypeCacheGroupOrigin || s.ParentCacheGroupID == 0) &&
		(s.ParentCacheGroupType == TypeCacheGroupOrigin || s.SecondaryParentCacheGroupID == 0)
}

// getServerInfo returns the necessary info about the server, whether the server exists, and any error.
func getServerInfoByID(tx *sql.Tx, id int) (*ServerInfo, bool, error) {
	return getServerInfo(tx, ServerInfoQuery+`WHERE s.id = $1`, []interface{}{id})
}

// getServerInfo returns the necessary info about the server, whether the server exists, and any error.
func getServerInfoByHost(tx *sql.Tx, host string) (*ServerInfo, bool, error) {
	return getServerInfo(tx, ServerInfoQuery+` WHERE s.host_name = $1 `, []interface{}{host})
}

// getServerInfo returns the necessary info about the server, whether the server exists, and any error.
func getServerInfo(tx *sql.Tx, qry string, qryParams []interface{}) (*ServerInfo, bool, error) {
	log.Errorf("getServerInfo qq "+qry+" p %++v\n", qryParams)
	s := ServerInfo{}
	if err := tx.QueryRow(qry, qryParams...).Scan(&s.CDN, &s.CDNID, &s.ID, &s.HostName, &s.DomainName, &s.IP, &s.ProfileID, &s.ProfileName, &s.Port, &s.Type, &s.CacheGroupID, &s.ParentCacheGroupID, &s.SecondaryParentCacheGroupID, &s.ParentCacheGroupType, &s.SecondaryParentCacheGroupType); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, errors.New("querying server info: " + err.Error())
	}
	return &s, true, nil
}

const ServerInfoQuery = `
SELECT
  c.name as cdn,
  s.cdn_id,
  s.id,
  s.host_name,
  c.domain_name,
  s.ip_address,
  s.profile AS profile_id,
  p.name AS profile_name,
  s.tcp_port,
  t.name as type,
  s.cachegroup,
  COALESCE(cg.parent_cachegroup_id, -1),
  COALESCE(cg.secondary_parent_cachegroup_id, -1),
  COALESCE(parentt.name, '') as parent_cachegroup_type,
  COALESCE(sparentt.name, '') as secondary_parent_cachegroup_type
FROM
  server s
  JOIN cdn c ON s.cdn_id = c.id
  JOIN type t ON s.type = t.id
  JOIN profile p ON p.id = s.profile
  JOIN cachegroup cg on s.cachegroup = cg.id
  LEFT JOIN type parentt on parentt.id = (select type from cachegroup where id = cg.parent_cachegroup_id)
  LEFT JOIN type sparentt on sparentt.id = (select type from cachegroup where id = cg.secondary_parent_cachegroup_id)
`

// GetATSMajorVersion returns the major version of the given profile's package trafficserver parameter.
// If no parameter exists, this does not return an error, but rather logs a warning and uses DefaultATSVersion.
func GetATSMajorVersion(tx *sql.Tx, serverProfileID ProfileID) (int, error) {
	atsVersion, _, err := GetProfileParamValue(tx, serverProfileID, "package", "trafficserver")
	if err != nil {
		return 0, errors.New("getting profile param value: " + err.Error())
	}
	if len(atsVersion) == 0 {
		atsVersion = DefaultATSVersion
		log.Warnln("Parameter package.trafficserver missing for profile " + strconv.Itoa(int(serverProfileID)) + ". Assuming version " + atsVersion)
	}
	atsMajorVer, err := strconv.Atoi(atsVersion[:1])
	if err != nil {
		return 0, errors.New("ats version parameter '" + atsVersion + "' on this profile is not a number (config_file 'package', name 'trafficserver')")
	}
	return atsMajorVer, nil
}

// GetProfileParamValue gets the value of a parameter assigned to a profile, by name and config file.
// Returns the parameter, whether it existed, and any error.
func GetProfileParamValue(tx *sql.Tx, profileID ProfileID, configFile string, name string) (string, bool, error) {
	qry := `
SELECT
  p.value
FROM
  parameter p
  JOIN profile_parameter pp ON p.id = pp.parameter
WHERE
  pp.profile = $1
  AND p.config_file = $2
  AND p.name = $3
`
	log.Errorf("DEBUG GetProfileParamValue calling with %v %v %v\n", profileID, configFile, name)
	val := ""
	if err := tx.QueryRow(qry, profileID, configFile, name).Scan(&val); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, errors.New("querying: " + err.Error())
	}
	return val, true, nil
}

type ParentConfigDS struct {
	Name            tc.DeliveryServiceName
	QStringIgnore   int
	OriginFQDN      string
	MultiSiteOrigin bool
	OriginShield    string
	Type            tc.DSType

	QStringHandling string
}

type ParentConfigDSTopLevel struct {
	ParentConfigDS
	MSOAlgorithm                       string
	MSOParentRetry                     string
	MSOUnavailableServerRetryResponses string
	MSOMaxSimpleRetries                string
	MSOMaxUnavailableServerRetries     string
}

const ParentConfigDSQuerySelect = `
SELECT
  ds.xml_id,
  COALESCE(ds.qstring_ignore, 0),
  COALESCE((SELECT o.protocol::text || '://' || o.fqdn || rtrim(concat(':', o.port::text), ':')
    FROM origin o
    WHERE o.deliveryservice = ds.id
    AND o.is_primary), '') as org_server_fqdn,
  COALESCE(ds.multi_site_origin, false),
  COALESCE(ds.origin_shield, ''),
  dt.name AS ds_type
`
const ParentConfigDSQueryFromTopLevel = `
FROM
  deliveryservice ds
  JOIN type as dt ON ds.type = dt.id
  JOIN cdn ON cdn.id = ds.cdn_id
` // TODO Perl does 'JOIN deliveryservice_regex dsr ON dsr.deliveryservice = ds.id   JOIN regex r ON dsr.regex = r.id   JOIN type as rt ON r.type = rt.id' and orders by, but doesn't use; ensure it isn't necessary

//debug
// const ParentConfigDSQueryFrom = ParentConfigDSQueryFromTopLevel + `
// JOIN deliveryservice_server dss ON dss.deliveryservice = ds.id
// `
const ParentConfigDSQueryFrom = ParentConfigDSQueryFromTopLevel + `
`

const ParentConfigDSQueryOrder = `
ORDER BY ds.id
` // TODO: perl does 'ORDER BY ds.id, rt.name, dsr.set_number' - but doesn't actually use regexes - ensure it isn't necessary

//debug
// const ParentConfigDSQueryWhere = `
// WHERE dss.server = $1
// `
const ParentConfigDSQueryWhere = `
WHERE ds.id in (SELECT DISTINCT(dss.deliveryservice) FROM deliveryservice_server dss where dss.server = $1)
`

const ParentConfigDSQueryWhereTopLevel = `
WHERE
  cdn.name = $1
  AND ds.id in (SELECT deliveryservice_server.deliveryservice FROM deliveryservice_server)
  AND ds.active = true
`

const ParentConfigDSQuery = ParentConfigDSQuerySelect +
	ParentConfigDSQueryFrom +
	ParentConfigDSQueryWhere +
	ParentConfigDSQueryOrder

const ParentConfigDSQueryTopLevel = ParentConfigDSQuerySelect +
	ParentConfigDSQueryFromTopLevel +
	ParentConfigDSQueryWhereTopLevel +
	ParentConfigDSQueryOrder

func getParentConfigDSTopLevel(tx *sql.Tx, cdnName tc.CDNName) ([]ParentConfigDSTopLevel, error) {
	dses, err := getParentConfigDSRaw(tx, ParentConfigDSQueryTopLevel, []interface{}{cdnName})
	if err != nil {
		return nil, errors.New("getting top level raw parent config ds: " + err.Error())
	}
	topDSes := []ParentConfigDSTopLevel{}
	for _, ds := range dses {
		topDSes = append(topDSes, ParentConfigDSTopLevel{ParentConfigDS: ds})
	}

	dsesWithParams, err := getParentConfigDSParamsTopLevel(tx, topDSes)
	if err != nil {
		return nil, errors.New("getting top level ds params: " + err.Error())
	}
	return dsesWithParams, nil
}

func getParentConfigDS(tx *sql.Tx, serverID int) ([]ParentConfigDS, error) {
	log.Errorln("DEBUG dupar query QQ" + ParentConfigDSQuery + "QQ")

	dses, err := getParentConfigDSRaw(tx, ParentConfigDSQuery, []interface{}{serverID})

	// debug
	seen := map[tc.DeliveryServiceName]struct{}{}
	log.Errorf("DEBUG dupar checking\n")
	for _, ds := range dses {
		if _, ok := seen[ds.Name]; ok {
			log.Errorf("DEBUG dupar %v\n", ds)
		}
		seen[ds.Name] = struct{}{}
	}
	log.Errorf("DEBUG dupar checked.\n")

	if err != nil {
		return nil, errors.New("getting raw parent config ds: " + err.Error())
	}

	dsesWithParams, err := getParentConfigDSParams(tx, dses)
	if err != nil {
		return nil, errors.New("getting ds params: " + err.Error())
	}
	return dsesWithParams, nil
}

// my $qstring = $self->profile_param_value( $server_obj->profile->id, 'parent.config', 'qstring', undef );
// 		my $parent_select_alg = $self->profile_param_value( $server_obj->profile->id, 'parent.config', 'algorithm', undef );
// my $qsh = $self->profile_param_value( $server_obj->profile->id, 'parent.config', 'psel.qstring_handling');

const ParentConfigParamQStringHandling = "psel.qstring_handling"
const ParentConfigParamMSOAlgorithm = "mso.algorithm"
const ParentConfigParamMSOParentRetry = "mso.parent_retry"
const ParentConfigParamUnavailableServerRetryResponses = "mso.unavailable_server_retry_responses"
const ParentConfigParamMaxSimpleRetries = "mso.max_simple_retries"
const ParentConfigParamMaxUnavailableServerRetryResponses = "mso.max_unavailable_server_retry_responses"
const ParentConfigParamAlgorithm = "algorithm"
const ParentConfigParamQString = "qstring"

func getParentConfigServerProfileParams(tx *sql.Tx, serverID int) (map[string]string, error) {
	qry := `
SELECT
  pa.name,
  pa.value
FROM
  parameter pa
  JOIN profile_parameter pp ON pp.parameter = pa.id
  JOIN profile pr ON pr.id = pp.profile
  JOIN server s on s.profile = pr.id
WHERE
  s.id = $1
  AND pa.config_file = 'parent.config'
  AND pa.name IN (
    '` + ParentConfigParamQStringHandling + `',
    '` + ParentConfigParamAlgorithm + `',
    '` + ParentConfigParamQString + `'
  )
`
	rows, err := tx.Query(qry, serverID)
	if err != nil {
		return nil, errors.New("querying: " + err.Error())
	}
	defer rows.Close()
	params := map[string]string{}
	for rows.Next() {
		name := ""
		val := ""
		if err := rows.Scan(&name, &val); err != nil {
			return nil, errors.New("scanning: " + err.Error())
		}
		params[name] = val
	}
	return params, nil
}

func getParentConfigDSRaw(tx *sql.Tx, qry string, qryParams []interface{}) ([]ParentConfigDS, error) {
	log.Errorf("DEBUG PCGen getParentConfigDSRaw qry qqq%+vqqq params %+v\n", qry, qryParams)
	rows, err := tx.Query(qry, qryParams...)
	if err != nil {
		return nil, errors.New("querying: " + err.Error())
	}
	defer rows.Close()
	dses := []ParentConfigDS{}
	for rows.Next() {
		d := ParentConfigDS{}
		if err := rows.Scan(&d.Name, &d.QStringIgnore, &d.OriginFQDN, &d.MultiSiteOrigin, &d.OriginShield, &d.Type); err != nil {
			return nil, errors.New("scanning: " + err.Error())
		}
		if d.OriginFQDN == "" {
			// TODO skip ANY_MAP DSes? Why? Did Perl, I didn't see it?
			log.Errorf("parent.config generation: getting parent config ds: server %+v has no origin, skipping!\n", d.Name)
			continue
		}
		d.Type = tc.DSTypeFromString(string(d.Type))
		dses = append(dses, d)
	}
	return dses, nil
}

func parentConfigDSesToNames(dses []ParentConfigDS) []string {
	names := []string{}
	for _, ds := range dses {
		names = append(names, string(ds.Name))
	}
	return names
}

func parentConfigDSesToNamesTopLevel(dses []ParentConfigDSTopLevel) []string {
	names := []string{}
	for _, ds := range dses {
		names = append(names, string(ds.Name))
	}
	return names
}

const ParentConfigDSParamsQuerySelect = `
SELECT
  ds.xml_id,
  pa.value,
  pa.name
`
const ParentConfigDSParamsQueryFrom = `
FROM
  parameter pa
  JOIN profile_parameter pp ON pp.parameter = pa.id
  JOIN profile pr ON pr.id = pp.profile
  JOIN deliveryservice ds on ds.profile = pr.id
`
const ParentConfigDSParamsQueryWhere = `
WHERE
  pa.config_file = 'parent.config'
  AND ds.xml_id = ANY($1)
  AND pa.name IN (
    '` + ParentConfigParamQStringHandling + `'
  )
`

var ParentConfigDSParamsQueryWhereTopLevel = `
WHERE
  pa.config_file = 'parent.config'
  AND ds.xml_id = ANY($1)
  AND pa.name IN (
    '` + ParentConfigParamQStringHandling + `',
    '` + ParentConfigParamMSOAlgorithm + `',
    '` + ParentConfigParamMSOParentRetry + `',
    '` + ParentConfigParamUnavailableServerRetryResponses + `',
    '` + ParentConfigParamMaxSimpleRetries + `',
    '` + ParentConfigParamMaxUnavailableServerRetryResponses + `'
  )
`

const ParentConfigDSParamsQuery = ParentConfigDSParamsQuerySelect + ParentConfigDSParamsQueryFrom + ParentConfigDSParamsQueryWhere

var ParentConfigDSParamsQueryTopLevel = ParentConfigDSParamsQuerySelect + ParentConfigDSParamsQueryFrom + ParentConfigDSParamsQueryWhereTopLevel

func getParentConfigDSParams(tx *sql.Tx, dses []ParentConfigDS) ([]ParentConfigDS, error) {
	params, err := getParentConfigDSParamsRaw(tx, ParentConfigDSParamsQuery, parentConfigDSesToNames(dses)) // (map[tc.DeliveryServiceName]map[string]string, error) {
	if err != nil {
		return nil, err
	}
	for i, ds := range dses {
		dsParams, ok := params[ds.Name]
		if !ok {
			continue // TODO warn?
		}
		if v, ok := dsParams[ParentConfigParamQStringHandling]; ok {
			ds.QStringHandling = v
			dses[i] = ds
		}
	}
	return dses, nil
}

func getParentConfigDSParamsTopLevel(tx *sql.Tx, dses []ParentConfigDSTopLevel) ([]ParentConfigDSTopLevel, error) {
	params, err := getParentConfigDSParamsRaw(tx, ParentConfigDSParamsQueryTopLevel, parentConfigDSesToNamesTopLevel(dses))
	if err != nil {
		return nil, err
	}
	for i, ds := range dses {
		dsParams, ok := params[ds.Name]
		if !ok {
			continue // TODO warn?
		}
		if v, ok := dsParams[ParentConfigParamQStringHandling]; ok {
			ds.QStringHandling = v
		}
		if v, ok := dsParams[ParentConfigParamMSOAlgorithm]; ok {
			ds.MSOAlgorithm = v
		}
		if v, ok := dsParams[ParentConfigParamMSOParentRetry]; ok {
			ds.MSOParentRetry = v
		}
		if v, ok := dsParams[ParentConfigParamUnavailableServerRetryResponses]; ok {
			ds.MSOUnavailableServerRetryResponses = v
		}
		if v, ok := dsParams[ParentConfigParamMaxSimpleRetries]; ok {
			ds.MSOMaxSimpleRetries = v
		}
		if v, ok := dsParams[ParentConfigParamMaxUnavailableServerRetryResponses]; ok {
			ds.MSOMaxUnavailableServerRetries = v
		}
		dses[i] = ds
	}
	return dses, nil
}

func getParentConfigDSParamsRaw(tx *sql.Tx, qry string, dsNames []string) (map[tc.DeliveryServiceName]map[string]string, error) {
	rows, err := tx.Query(qry, pq.Array(dsNames))
	if err != nil {
		return nil, errors.New("querying: " + err.Error())
	}
	defer rows.Close()

	params := map[tc.DeliveryServiceName]map[string]string{}
	for rows.Next() {
		dsName := tc.DeliveryServiceName("")
		pName := ""
		pVal := ""
		if err := rows.Scan(&dsName, &pName, &pVal); err != nil {
			return nil, errors.New("scanning: " + err.Error())
		}
		if _, ok := params[dsName]; !ok {
			params[dsName] = map[string]string{}
		}
		params[dsName][pName] = pVal
	}
	return params, nil
}

type ParentInfo struct {
	Host            string
	Port            int
	Domain          string
	Weight          float64
	UseIP           bool
	Rank            int
	IP              string
	PrimaryParent   bool
	SecondaryParent bool
}

// parentData parent_data in Perl
func getParentInfo(tx *sql.Tx, server *ServerInfo) (map[string][]ParentInfo, error) {
	log.Errorf("DEBUG PCGen getParentInfo\n")
	parentInfos := map[string][]ParentInfo{}

	serverDomain, ok, err := getCDNDomainByProfileID(tx, server.ProfileID)
	if err != nil {
		return nil, errors.New("getting CDN domain from profile ID: " + err.Error())
	} else if !ok || serverDomain == "" {
		return parentInfos, nil // TODO warn? Perl doesn't.
	}
	log.Errorf("DEBUG PCGen getParentInfo got CDN domain '%+v'\n", serverDomain)

	profileCaches, deliveryServices, err := getServerParentCacheGroupProfiles(tx, server)
	if err != nil {
		return nil, err
	}

	log.Errorf("DEBUG PCGen getParentInfo got profileCaches len '%+v'\n", len(profileCaches))
	log.Errorf("DEBUG PCGen getParentInfo got deliveryServices len '%+v'\n", len(deliveryServices))

	log.Errorf("DEBUG PCGen getParentInfo got profileCaches '%++v'\n", profileCaches)
	log.Errorf("DEBUG PCGen getParentInfo got deliveryServices '%++v'\n", deliveryServices)

	// note deliveryServies also contains an "all" key
	// originFQDN is "prefix" in Perl; ds is not really a "ds", that's what it's named in Perl
	for originFQDN, dses := range deliveryServices {
		for _, row := range dses {
			profile := profileCaches[row.ProfileID]
			if profile.NotAParent {
				log.Errorf("DEBUG PCGen getParentInfo row " + row.ServerHost + " not a parent, skipping\n")
				continue
			}
			// Perl has this check, but we only select "deliveryServices" (servers) with the right CDN in the first place
			// if profile.Domain != serverDomain {
			// 	continue
			// }

			parentInf := ParentInfo{
				Host:            row.ServerHost,
				Port:            profile.Port,
				Domain:          row.Domain,
				Weight:          profile.Weight,
				UseIP:           profile.UseIP,
				Rank:            profile.Rank,
				IP:              row.ServerIP,
				PrimaryParent:   server.ParentCacheGroupID == row.CacheGroupID,
				SecondaryParent: server.SecondaryParentCacheGroupID == row.CacheGroupID,
			}
			if parentInf.Port < 1 {
				parentInf.Port = row.ServerPort
			}
			parentInfos[originFQDN] = append(parentInfos[originFQDN], parentInf)
		}
	}
	return parentInfos, nil
}

// deliveryservices map[orgURI.Host]

type ProfileCache struct {
	Weight     float64
	Port       int
	UseIP      bool
	Rank       int
	NotAParent bool
}

func DefaultProfileCache() ProfileCache {
	return ProfileCache{
		Weight:     0.999,
		Port:       0,
		UseIP:      false,
		Rank:       1,
		NotAParent: false,
	}
}

// TODO rename
type CGPDeliveryService struct {
	ServerID     ServerID
	ServerHost   string
	ServerIP     string
	ServerPort   int
	CacheGroupID int
	Status       int
	Type         int
	ProfileID    ProfileID
	CDN          int
	TypeName     string
	Domain       string
}

// getServerParentCacheGroupProfiles gets the profile information for servers belonging to the parent cachegroup, and secondary parent cachegroup, of the cachegroup of each server.
func getServerParentCacheGroupProfiles(tx *sql.Tx, server *ServerInfo) (map[ProfileID]ProfileCache, map[string][]CGPDeliveryService, error) {
	// TODO make this more efficient - should be a single query - this was transliterated from Perl - it's extremely inefficient.

	profileCaches := map[ProfileID]ProfileCache{}
	deliveryServices := map[string][]CGPDeliveryService{}

	qry := ""
	if server.IsTopLevelCache() {
		// multisite origins take all the org groups in to account
		qry = `
WITH parent_cachegroup_ids AS (
  SELECT id from cachegroup
  JOIN type on type.id = cachegroup.type
  WHERE type.name = '` + TypeCacheGroupOrigin + `'
),
`
	} else {
		qry = `
WITH server_cachegroup_ids AS (
  SELECT cachegroup as v FROM server WHERE id = $1
),
parent_cachegroup_ids AS (
  SELECT parent_cachegroup_id as v
  FROM cachegroup WHERE id IN (SELECT v from server_cachegroup_ids)
  UNION ALL
  SELECT secondary_parent_cachegroup_id as v
  FROM cachegroup WHERE id IN (SELECT v from server_cachegroup_ids)
)
`
	}

	qry += `
SELECT
  s.id,
  s.host_name,
  s.ip_address,
  s.tcp_port,
  s.cachegroup,
  s.status,
  s.type,
  s.profile,
  s.cdn_id,
  stype.name as type_name,
  s.domain_name
FROM
  server s
  JOIN type stype ON s.type = stype.id
  JOIN cachegroup cg ON cg.id = s.cachegroup
  JOIN cdn on s.cdn_id = cdn.id
  JOIN status st ON st.id = s.status
WHERE
  cg.id IN (SELECT v FROM parent_cachegroup_ids)
  AND (stype.name = 'ORG' OR stype.name LIKE 'EDGE%' OR stype.name LIKE 'MID%')
  AND (st.name = 'REPORTED' OR st.name = 'ONLINE')
  AND cdn.name = $2
`
	rows, err := tx.Query(qry, server.ID, server.CDN)
	if err != nil {
		return nil, nil, errors.New("querying: " + err.Error())
	}
	defer rows.Close()

	cgServerIDs := []int{}
	ss := []CGPDeliveryService{}
	for rows.Next() {
		s := CGPDeliveryService{}
		if err := rows.Scan(&s.ServerID, &s.ServerHost, &s.ServerIP, &s.ServerPort, &s.CacheGroupID, &s.Status, &s.Type, &s.ProfileID, &s.CDN, &s.TypeName, &s.Domain); err != nil {
			return nil, nil, errors.New("scanning: " + err.Error())
		}
		ss = append(ss, s)
		cgServerIDs = append(cgServerIDs, int(s.ServerID))
	}

	cgServerDSes, err := getServerDSes(tx, cgServerIDs)
	if err != nil {
		return nil, nil, errors.New("getting cachegroup server deliveryservices: " + err.Error())
	}

	profileParams, err := getParentConfigServerProfileParamsX(tx, cgServerIDs) // TODO change to take cg IDs directly?
	if err != nil {
		return nil, nil, errors.New("getting cachegroup server profile params: " + err.Error())
	}

	allDSMap := map[DeliveryServiceID]struct{}{}
	for _, dses := range cgServerDSes {
		for _, ds := range dses {
			allDSMap[ds] = struct{}{}
		}
	}
	allDSes := []int{}
	for ds, _ := range allDSMap {
		allDSes = append(allDSes, int(ds))
	}

	dsOrigins, err := getDSOrigins(tx, allDSes)
	if err != nil {
		return nil, nil, errors.New("getting deliveryservice origins: " + err.Error())
	}

	for _, s := range ss {
		if s.TypeName == TypeOrigin {
			dses := cgServerDSes[s.ServerID]
			for _, ds := range dses {
				orgURI := dsOrigins[ds]
				deliveryServices[orgURI.Host] = append(deliveryServices[orgURI.Host], s)
			}
		} else {
			deliveryServices[DeliveryServicesAllParentsKey] = append(deliveryServices[DeliveryServicesAllParentsKey], s)
		}

		if _, profileCachesHasProfile := profileCaches[s.ProfileID]; !profileCachesHasProfile {
			defaultProfileCache := DefaultProfileCache()
			if profileCache, profileParamsHasProfile := profileParams[s.ProfileID]; !profileParamsHasProfile {
				log.Warnf("cachegroup has server with profile %+v but that profile has no parameters", s.ProfileID)
				profileCaches[s.ProfileID] = defaultProfileCache
			} else {
				profileCaches[s.ProfileID] = profileCache
			}
		}
	}
	return profileCaches, deliveryServices, nil
}

// TODO change, this is terrible practice, using a hard-coded key. What if there were a delivery service named "all_parents" (transliterated Perl)
const DeliveryServicesAllParentsKey = "all_parents"

const TypeOrigin = "ORG" // TODO move to lib/go-tc

type ServerID int

func getServerDSes(tx *sql.Tx, serverIDs []int) (map[ServerID][]DeliveryServiceID, error) {
	sds := map[ServerID][]DeliveryServiceID{}
	if len(serverIDs) == 0 {
		return sds, nil
	}
	qry := `
SELECT
  dss.server,
  dss.deliveryservice
FROM
  deliveryservice_server dss
WHERE
  dss.server = ANY($1)
`
	rows, err := tx.Query(qry, pq.Array(serverIDs))
	if err != nil {
		return nil, errors.New("querying: " + err.Error())
	}
	defer rows.Close()

	for rows.Next() {
		sID := ServerID(0)
		dsID := DeliveryServiceID(0)
		if err := rows.Scan(&sID, &dsID); err != nil {
			return nil, errors.New("scanning: " + err.Error())
		}
		sds[sID] = append(sds[sID], dsID)
	}
	return sds, nil
}

type DeliveryServiceID int

type OriginURI struct {
	Scheme string
	Host   string
	Port   string
}

func getDSOrigins(tx *sql.Tx, dsIDs []int) (map[DeliveryServiceID]*OriginURI, error) {
	origins := map[DeliveryServiceID]*OriginURI{}
	if len(dsIDs) == 0 {
		return origins, nil
	}
	qry := `
SELECT
  ds.id,
  o.protocol::text,
  o.fqdn,
  COALESCE(o.port::text, "")
FROM
  deliveryservice ds
  JOIN origin o ON o.deliveryservice = ds.id
WHERE
  ds.id = ANY($1)
  AND o.is_primary
`
	rows, err := tx.Query(qry, pq.Array(dsIDs))
	if err != nil {
		return nil, errors.New("querying: " + err.Error())
	}
	defer rows.Close()

	for rows.Next() {
		id := DeliveryServiceID(0)
		uri := &OriginURI{}
		if err := rows.Scan(&id, &uri.Scheme, &uri.Host, &uri.Port); err != nil {
			return nil, errors.New("scanning: " + err.Error())
		}
		if uri.Port == "" {
			if uri.Scheme == "http" {
				uri.Port = "80"
			} else if uri.Scheme == "https" {
				uri.Port = "443"
			} else {
				log.Warnf("parent.config generation: origin had unknown scheme '" + uri.Scheme + "' and no port; leaving port empty")
			}
		}
		origins[id] = uri
	}
	return origins, nil
}

const ParentConfigCacheParamWeight = "weight"
const ParentConfigCacheParamPort = "port"
const ParentConfigCacheParamUseIP = "use_ip_address"
const ParentConfigCacheParamRank = "rank"
const ParentConfigCacheParamNotAParent = "not_a_parent"

type ProfileID int

// TODO rename
func getParentConfigServerProfileParamsX(tx *sql.Tx, serverIDs []int) (map[ProfileID]ProfileCache, error) {
	qry := `
SELECT
  pr.id,
  pa.name,
  pa.value
FROM
  parameter pa
  JOIN profile_parameter pp ON pp.parameter = pa.id
  JOIN profile pr ON pr.id = pp.profile
  JOIN server s on s.profile = pr.id
WHERE
  s.id = ANY($1)
  AND pa.config_file = 'parent.config'
  AND pa.name IN (
    '` + ParentConfigCacheParamWeight + `',
    '` + ParentConfigCacheParamPort + `',
    '` + ParentConfigCacheParamUseIP + `',
    '` + ParentConfigCacheParamRank + `',
    '` + ParentConfigCacheParamNotAParent + `'
  )
`
	rows, err := tx.Query(qry, pq.Array(serverIDs))
	if err != nil {
		return nil, errors.New("querying: " + err.Error())
	}
	defer rows.Close()

	type Param struct {
		ProfileID ProfileID
		Name      string
		Val       string
	}

	params := []Param{}
	for rows.Next() {
		p := Param{}
		if err := rows.Scan(&p.ProfileID, &p.Name, &p.Val); err != nil {
			return nil, errors.New("scanning: " + err.Error())
		}
		params = append(params, p)
	}

	sParams := map[ProfileID]ProfileCache{} // TODO change to map of pointers? Does efficiency matter?
	for _, param := range params {
		profileCache, ok := sParams[param.ProfileID]
		if !ok {
			profileCache = DefaultProfileCache()
		}
		switch param.Name {
		case ParentConfigCacheParamWeight:
			f, err := strconv.ParseFloat(param.Val, 64)
			if err != nil {
				log.Errorln("parent.config generation: weight param is not a float, skipping! : " + err.Error())
			} else {
				profileCache.Weight = f
			}
		case ParentConfigCacheParamPort:
			i, err := strconv.ParseInt(param.Val, 10, 64)
			if err != nil {
				log.Errorln("parent.config generation: port param is not an integer, skipping! : " + err.Error())
			} else {
				profileCache.Port = int(i)
			}
		case ParentConfigCacheParamUseIP:
			profileCache.UseIP = param.Val == "1"
		case ParentConfigCacheParamRank:
			i, err := strconv.ParseInt(param.Val, 10, 64)
			if err != nil {
				log.Errorln("parent.config generation: rank param is not an integer, skipping! : " + err.Error())
			} else {
				profileCache.Rank = int(i)
			}

		case ParentConfigCacheParamNotAParent:
			profileCache.NotAParent = param.Val != "false"
		default:
			return nil, errors.New("query returned unexpected param: " + param.Name)
		}
		sParams[param.ProfileID] = profileCache
	}
	return sParams, nil
}

func getServerParams(tx *sql.Tx, serverID int) (map[string]string, error) {
	qry := `
SELECT
  pa.name
  pa.value
FROM
  parameter pa
  JOIN profile_parameter pp ON pp.parameter = pa.id
  JOIN profile pr ON pr.id = pp.profile
  JOIN server s on s.profile = pr.id
WHERE
  s.id = $1
  AND pa.config_file = 'parent.config'
  AND pa.name IN (
    '` + ParentConfigParamQStringHandling + `',
    '` + ParentConfigParamAlgorithm + `',
    '` + ParentConfigParamQString + `'
  )
`
	rows, err := tx.Query(qry, serverID)
	if err != nil {
		return nil, errors.New("querying: " + err.Error())
	}
	defer rows.Close()
	params := map[string]string{}
	for rows.Next() {
		name := ""
		val := ""
		if err := rows.Scan(&name, &val); err != nil {
			return nil, errors.New("scanning: " + err.Error())
		}
		params[name] = val
	}
	return params, nil
}

type ParentConfigServerParams struct {
	QString         string
	Algorithm       string
	QStringHandling bool
}

func getCDNDomainByProfileID(tx *sql.Tx, profileID ProfileID) (string, bool, error) {
	qry := `SELECT domain_name from cdn where id = (select cdn from profile where id = $1)`
	val := ""
	if err := tx.QueryRow(qry, profileID).Scan(&val); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, errors.New("querying: " + err.Error())
	}
	return val, true, nil
}
