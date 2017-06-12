var map;

var cacheUnknownColor = '#000000';
var cacheAvailableColor = '#009900';
var cacheOfflineColor = '#FFBB00';
var cacheUnavailableColor = '#FF0000';

var iconSize = [25, 25];
var cgIcon = L.icon({
  iconUrl: 'cg-grey.png',
  iconSize:     iconSize,
  iconAnchor:   [0, 0],
  popupAnchor:  [10, 2]
});
var cgErrIcon = L.icon({
  iconUrl: 'cg-red.png',
  iconSize:     iconSize,
  iconAnchor:   [0, 0],
  popupAnchor:  [-3, -76]
});
var cgWarnIcon = L.icon({
  iconUrl: 'cg-orange.png',
  iconSize:     iconSize,
  iconAnchor:   [0, 0],
  popupAnchor:  [-3, -76]
});
var servers = {};
var cachegroups = {};
var cachegroupMarkers = {};
var cacheCachegroups = {};
var cachegroupCaches = {};
var cachePopupElems = {};

var cdns = {};
var cdnServerLayerGroups = {};
var overlayMaps = {};

function ajax(url, callback){
  var xmlhttp = new XMLHttpRequest();
  xmlhttp.onreadystatechange = function(){
    if (xmlhttp.readyState == 4 && xmlhttp.status == 200){
      callback(xmlhttp.responseText);
    }
  }
  xmlhttp.open("GET", url, true);
  xmlhttp.send();
}

function initMap(tileUrl) {
  map = new L.Map('map0');

  var osmAttrib='Map data © <a href="http://openstreetmap.org">OpenStreetMap</a> contributors'; // TODO fix? I'm hesitant to make this a parameter which can be omitted, encouraging OSM TOS violations.
  var osm = new L.TileLayer(tileUrl, {minZoom: 2, maxZoom: 16, attribution: osmAttrib});
  map.setView(new L.LatLng(39.73, -104.98),5);
  map.addLayer(osm);
}

function getCachegroupMarkerPopup(cg) {
  var div = document.createElement("div");

  var b = document.createElement("b");
  div.appendChild(b);

  var txt = document.createTextNode(cg.name);
  b.appendChild(txt);

  var br = document.createElement("br");
  div.appendChild(br);

  return div
}

function addCache(cachegroupMarkerPopupContent, cacheName) {
  var span = document.createElement("span");
  span.style.color = cacheUnknownColor;
  span.style.margin = "10px";
  var txt = document.createTextNode(cacheName);
  span.appendChild(txt);
  cachegroupMarkerPopupContent.appendChild(span);

  cachePopupElems[cacheName] = span;
  return cachegroupMarkerPopupContent;
}

function addMarker(latlon, popupHtml) {
  var m0 = L.marker(latlon).addTo(map);
  m0.bindPopup(popupHtml);
}

function getStates() {
  ajax("/publish/CrStates", function(srvTxt) {
    var rawStates = JSON.parse(srvTxt);
    var cacheStates = rawStates["caches"];
    for(var cacheName in cacheStates) {
      if (!cacheStates.hasOwnProperty(cacheName)) {
        continue; // skip prototype properties
      }

      var cacheElem = cachePopupElems[cacheName];
      if(typeof cacheElem == "undefined") {
        // console.log("ERROR: cache " + cacheName + " has no element!"); // DEBUG
        continue
      }
      var available = cacheStates[cacheName].isAvailable;
      if(available) {
        cacheElem.style.color = cacheAvailableColor;
        cacheElem.style.fontWeight = 'normal';
      } else {
        /* console.log("cache " + cacheName + " is " + available); */
        cacheElem.style.color = cacheUnavailableColor;
        cacheElem.style.fontWeight = 'bold';
      }
    }
  })
}

// function hostnameFromFqdn(fqdn) {
//   var dotPos = fqdn.indexOf(".");
//   if(dotPos == -1) {
//     return fqdn;
//   }
//   var hostname = fqdn.substring(0, dotPos);
//   return hostname;
// }

function addServerToMarker(server, cdnName) {
  var cacheName = server.hostName;
  var cgName = server.cachegroup;
	var marker = cachegroupMarkers[cdnName][cgName];
  if(typeof marker == "undefined") {
    console.log("ERROR no cachegroup for " + cgName);
    return;
  }
  var popup = marker.getPopup();
  var popupContent = popup.getContent();
  popupContent = addCache(popupContent, cacheName);
  popup.setContent(popupContent); // TODO necessary?
  popup.update(); // TODO update once per popup? Necessary?
}

function getServers() {
  ajax("/api/1.2/servers.json", function(srvTxt) {
    var rawServers = JSON.parse(srvTxt);
    servers = rawServers["response"];
    for(var i = 0; i < servers.length; i++) {
      var s = servers[i];
      var cacheName = s.hostName;
      var cgName = s.cachegroup;
			var cdnName = s.cdnName;
			// console.log("getServers cache " + cacheName + " cdn " + cdnName + " cg " + cgName); // DEBUG

			addServerToMarker(s, cdnName);
			addServerToMarker(s, "ALL");

      cacheCachegroups[cacheName] = cgName;
      if(typeof cachegroupCaches[cgName] == "undefined") {
        cachegroupCaches[cgName] = [];
      }
      cachegroupCaches[cgName].push(cgName);
    }
    getStates()
  })
}

function getCachegroups() {
  ajax("/api/1.2/cachegroups.json", function(cgTxt) {
    var rawCachegroups = JSON.parse(cgTxt);
    cachegroups = rawCachegroups["response"];
		for(var i = 0; i < cdns.length; i++) {
			var cdn = cdns[i];
			// console.log("cachegroupMarkers cdn " + cdn.name); // DEBUG
			cachegroupMarkers[cdn.name] = {};
			for(var j = 0; j < cachegroups.length; j++) {
				var cg = cachegroups[j];
				var marker = L.marker([cg.latitude, cg.longitude], {icon: cgIcon});
				var popup = marker.bindPopup(getCachegroupMarkerPopup(cg));
				cachegroupMarkers[cdn.name][cg.name] = marker;
				// console.log("cdnServerLayerGroups " + cdn.name + " cg " + cg.name); // DEBUG
				cdnServerLayerGroups[cdn.name].addLayer(marker)
			}
    }
    getServers(); // TODO concurrently request with cachegroups
  })
}

function getCDNs() {
  ajax("/api/1.2/cdns.json", function(txt) {
    var raw = JSON.parse(txt);
    cdns = raw["response"];
    for(var i = 0; i < cdns.length; i++) {
      var cdn = cdns[i];
			// console.log("cdnServerLayerGroups adding " + cdn.name); // DEBUG
			var lg = L.layerGroup().addTo(map);
			cdnServerLayerGroups[cdn.name] = lg;
			overlayMaps[cdn.name] = lg
		}
		L.control.layers(null, overlayMaps).addTo(map);
		getCachegroups();
	})
}

function init(tileUrl) {
  initMap(tileUrl);
	getCDNs();
}
