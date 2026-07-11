import { extendLeaflet } from 'https://esm.sh/@india-boundary-corrector/leaflet-layer';

/**
 * Pravaaha — app.js
 * Frontend logic: Leaflet map, geolocation, routing, flood visualization
 */

// Extend Leaflet with the corrector
extendLeaflet(L);

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------
const MANGALORE_CENTER = [12.87, 74.88];
const DEFAULT_ZOOM = 13;
const API_BASE = '';  // Same origin

// ---------------------------------------------------------------------------
// Map Initialization
// ---------------------------------------------------------------------------
const map = L.map('map', {
    center: MANGALORE_CENTER,
    zoom: DEFAULT_ZOOM,
    zoomControl: true,
    attributionControl: true,
});

// Tile Layer state tracking
let activeTileLayer = null;

function setTileLayer(theme) {
    if (activeTileLayer) {
        map.removeLayer(activeTileLayer);
    }
    
    // Choose CartoDB Positron for light theme, Dark Matter for dark theme
    const tileUrl = theme === 'light'
        ? 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png'
        : 'https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png';
        
    activeTileLayer = L.tileLayer.indiaBoundaryCorrected(tileUrl, {
        attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OSM</a> contributors &copy; <a href="https://carto.com/">CARTO</a>',
        subdomains: 'abcd',
        maxZoom: 19,
    }).addTo(map);
}

// Initial dark theme map setup
setTileLayer('dark');

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------
let userLatLng = null;
let destLatLng = null;
let userMarker = null;
let destMarker = null;
let routePolyline = null;
let floodLayers = [];
let isFloodActive = false;
let weatherCondition = 'clear'; // 'clear' or 'monsoon'

// ---------------------------------------------------------------------------
// DOM References
// ---------------------------------------------------------------------------
const btnRoute = document.getElementById('btn-route');
const btnFlood = document.getElementById('btn-flood');
const btnClear = document.getElementById('btn-clear');
const btnClearRoute = document.getElementById('btn-clear-route');
const inputStart = document.getElementById('input-start');
const btnSearchStart = document.getElementById('btn-search-start');
const inputEnd = document.getElementById('input-end');
const btnSearchEnd = document.getElementById('btn-search-end');
const statusDot = document.getElementById('status-dot');
const statusText = document.getElementById('status-text');
const routeInfo = document.getElementById('route-info');
const routeDistance = document.getElementById('route-distance');
const routeNodes = document.getElementById('route-nodes');
const floodStatusEl = document.getElementById('flood-status');
const loadingOverlay = document.getElementById('loading-overlay');
const loadingText = document.getElementById('loading-text');

// New Controls
const toggleMonsoon = document.getElementById('toggle-monsoon');
const toggleTheme = document.getElementById('toggle-theme');
const btnGps = document.getElementById('btn-gps');

let clickState = 'start';

// ---------------------------------------------------------------------------
// Custom Markers
// ---------------------------------------------------------------------------
function createUserIcon() {
    return L.divIcon({
        className: 'user-marker',
        html: `<div class="user-marker-pulse"></div><div class="user-marker-dot"></div>`,
        iconSize: [40, 40],
        iconAnchor: [20, 20],
    });
}

function createDestIcon() {
    return L.divIcon({
        className: '',
        html: `<div class="dest-marker"></div>`,
        iconSize: [14, 14],
        iconAnchor: [7, 7],
    });
}

// ---------------------------------------------------------------------------
// Geolocation & Reverse Geocoding
// ---------------------------------------------------------------------------
function initGeolocation() {
    if (!navigator.geolocation) {
        setUserPosition(MANGALORE_CENTER[0], MANGALORE_CENTER[1]);
        updateStatus('Geolocation unavailable — using default', 'warning');
        return;
    }

    navigator.geolocation.getCurrentPosition(
        (pos) => {
            const { latitude, longitude } = pos.coords;
            // Check if user is roughly near Mangalore (within ~50km)
            const dist = Math.sqrt(
                Math.pow(latitude - MANGALORE_CENTER[0], 2) +
                Math.pow(longitude - MANGALORE_CENTER[1], 2)
            );
            if (dist > 0.5) {
                // User is far from Mangalore, use default center
                setUserPosition(MANGALORE_CENTER[0], MANGALORE_CENTER[1]);
                updateStatus('Location outside Mangalore — using default', 'warning');
            } else {
                setUserPosition(latitude, longitude);
                updateStatus('Location acquired', 'ok');
            }
        },
        () => {
            setUserPosition(MANGALORE_CENTER[0], MANGALORE_CENTER[1]);
            updateStatus('Location denied — using default', 'warning');
        },
        { enableHighAccuracy: true, timeout: 8000 }
    );
}

function setUserPosition(lat, lon) {
    setStartMarker(lat, lon);
    map.setView([lat, lon], DEFAULT_ZOOM);
}

// Fetch user geolocation on crosshair click and reverse-geocode using Nominatim
async function handleGpsSnapping() {
    if (!navigator.geolocation) {
        updateStatus('Geolocation unavailable on this device', 'warning');
        return;
    }

    showLoading('Snapping current GPS location...');
    navigator.geolocation.getCurrentPosition(
        async (pos) => {
            const { latitude, longitude } = pos.coords;
            
            // Check if coordinates snap near Mangalore
            const dist = Math.sqrt(
                Math.pow(latitude - MANGALORE_CENTER[0], 2) +
                Math.pow(longitude - MANGALORE_CENTER[1], 2)
            );

            if (dist > 0.5) {
                setUserPosition(MANGALORE_CENTER[0], MANGALORE_CENTER[1]);
                inputStart.value = 'Mangalore, India';
                updateStatus('Location outside Mangalore — using center', 'warning');
                hideLoading();
            } else {
                setStartMarker(latitude, longitude);
                map.setView([latitude, longitude], DEFAULT_ZOOM);
                updateStatus('Resolving address...', 'ok');
                
                try {
                    const res = await fetch(`https://nominatim.openstreetmap.org/reverse?format=json&lat=${latitude}&lon=${longitude}&zoom=18&addressdetails=1`);
                    if (res.ok) {
                        const data = await res.json();
                        const addressText = data.display_name || `${latitude.toFixed(5)}, ${longitude.toFixed(5)}`;
                        inputStart.value = addressText;
                        updateStatus('Address resolved from coordinates', 'ok');
                    } else {
                        inputStart.value = `${latitude.toFixed(5)}, ${longitude.toFixed(5)}`;
                        updateStatus('GPS coordinates snap set (address lookup failed)', 'warning');
                    }
                } catch (err) {
                    inputStart.value = `${latitude.toFixed(5)}, ${longitude.toFixed(5)}`;
                    updateStatus('GPS coordinates snap set (network error)', 'warning');
                } finally {
                    hideLoading();
                }
            }
        },
        (err) => {
            updateStatus(`GPS snapping failed: ${err.message}`, 'danger');
            hideLoading();
        },
        { enableHighAccuracy: true, timeout: 8000 }
    );
}

// ---------------------------------------------------------------------------
// Destination Selection
// ---------------------------------------------------------------------------
map.on('click', (e) => {
    if (clickState === 'start') {
        setStartMarker(e.latlng.lat, e.latlng.lng);
        clickState = 'end';
        updateStatus('Start location set. Click map to set destination.', 'ok');
    } else {
        setEndMarker(e.latlng.lat, e.latlng.lng);
        clickState = 'start';
        updateStatus('Destination set — ready to route', 'ok');
    }
});

function setStartMarker(lat, lon) {
    userLatLng = [lat, lon];
    if (userMarker) {
        userMarker.setLatLng(userLatLng);
    } else {
        userMarker = L.marker(userLatLng, { icon: createUserIcon() }).addTo(map).bindPopup('<b>📍 Start Location</b>');
    }
    userMarker.openPopup();
    if (inputStart && !inputStart.value) {
        inputStart.value = `${lat.toFixed(4)}, ${lon.toFixed(4)}`;
    }
    clickState = 'end';
    checkRouteReady();
}

function setEndMarker(lat, lon) {
    destLatLng = [lat, lon];
    if (destMarker) {
        destMarker.setLatLng(destLatLng);
    } else {
        destMarker = L.marker(destLatLng, { icon: createDestIcon() }).addTo(map).bindPopup('<b>🎯 Destination</b>');
    }
    destMarker.openPopup();
    if (inputEnd) inputEnd.value = `${lat.toFixed(4)}, ${lon.toFixed(4)}`;
    checkRouteReady();
}

function checkRouteReady() {
    if (userLatLng && destLatLng) {
        btnRoute.disabled = false;
    } else {
        btnRoute.disabled = true;
    }
}

async function searchLocation(type) {
    const input = type === 'start' ? inputStart : inputEnd;
    const query = input.value.trim();
    if (!query) return;

    showLoading(`Searching for location...`);
    try {
        const res = await fetch(`https://nominatim.openstreetmap.org/search?format=json&q=${encodeURIComponent(query)}&limit=1`);
        const data = await res.json();
        if (data && data.length > 0) {
            const lat = parseFloat(data[0].lat);
            const lon = parseFloat(data[0].lon);
            if (type === 'start') {
                setStartMarker(lat, lon);
                map.setView([lat, lon], DEFAULT_ZOOM);
                updateStatus('Start location found', 'ok');
            } else {
                setEndMarker(lat, lon);
                map.setView([lat, lon], DEFAULT_ZOOM);
                updateStatus('Destination found — ready to route', 'ok');
            }
        } else {
            updateStatus('Location not found', 'warning');
        }
    } catch (err) {
        console.error(err);
        updateStatus('Search failed', 'danger');
    } finally {
        hideLoading();
    }
}

function clearSelection() {
    if (userMarker) {
        map.removeLayer(userMarker);
        userMarker = null;
    }
    if (destMarker) {
        map.removeLayer(destMarker);
        destMarker = null;
    }
    userLatLng = null;
    destLatLng = null;
    if (inputStart) inputStart.value = '';
    if (inputEnd) inputEnd.value = '';
    clickState = 'start';
    clearRoute();
    routeInfo.style.display = 'none';
    checkRouteReady();
    updateStatus('Selection cleared', 'ok');
}

// ---------------------------------------------------------------------------
// Ripple effect on buttons
// ---------------------------------------------------------------------------
function initRippleEffect() {
    document.querySelectorAll('.btn').forEach((btn) => {
        btn.addEventListener('mousemove', (e) => {
            const rect = btn.getBoundingClientRect();
            const x = ((e.clientX - rect.left) / rect.width) * 100;
            const y = ((e.clientY - rect.top) / rect.height) * 100;
            btn.style.setProperty('--x', x + '%');
            btn.style.setProperty('--y', y + '%');
        });
    });
}

// ---------------------------------------------------------------------------
// Loading UI
// ---------------------------------------------------------------------------
function showLoading(text = 'Computing route...') {
    loadingText.textContent = text;
    loadingOverlay.style.display = 'flex';
}

function hideLoading() {
    loadingOverlay.style.display = 'none';
}

// ---------------------------------------------------------------------------
// Status Updates
// ---------------------------------------------------------------------------
function updateStatus(text, level = 'ok') {
    statusText.textContent = text;
    statusDot.className = 'status-dot';
    statusDot.classList.add(`status-${level}`);
}

// ---------------------------------------------------------------------------
// Route Drawing
// ---------------------------------------------------------------------------
function drawRoute(coords) {
    if (routePolyline) {
        map.removeLayer(routePolyline);
    }

    routePolyline = L.polyline(coords, {
        color: '#22d3ee',
        weight: 5,
        opacity: 0.9,
        smoothFactor: 1.5,
        lineCap: 'round',
        lineJoin: 'round',
        dashArray: null,
        className: 'route-line',
    }).addTo(map);

    // Add a glow effect underneath
    const glowLine = L.polyline(coords, {
        color: '#22d3ee',
        weight: 14,
        opacity: 0.15,
        smoothFactor: 1.5,
        lineCap: 'round',
        interactive: false,
    }).addTo(map);

    // Store glow reference on the main polyline for cleanup
    routePolyline._glowLine = glowLine;

    // Fit map to route
    map.fitBounds(routePolyline.getBounds(), { padding: [80, 80] });
}

function clearRoute() {
    if (routePolyline) {
        if (routePolyline._glowLine) {
            map.removeLayer(routePolyline._glowLine);
        }
        map.removeLayer(routePolyline);
        routePolyline = null;
    }
}

// ---------------------------------------------------------------------------
// Flood Visualization
// ---------------------------------------------------------------------------
function drawFloodZones(geometries) {
    clearFloodZones();

    geometries.forEach((segment) => {
        // Glowing red hazard line
        const hazardGlow = L.polyline(segment, {
            color: '#ef4444',
            weight: 18,
            opacity: 0.2,
            lineCap: 'round',
            interactive: false,
        }).addTo(map);

        const hazardLine = L.polyline(segment, {
            color: '#ef4444',
            weight: 6,
            opacity: 0.85,
            lineCap: 'round',
            dashArray: '8, 12',
        }).addTo(map).bindPopup('⚠️ <b>Flooded Road</b><br>Waterlogging detected');

        floodLayers.push(hazardGlow, hazardLine);
    });
}

function clearFloodZones() {
    floodLayers.forEach((layer) => map.removeLayer(layer));
    floodLayers = [];
}

// ---------------------------------------------------------------------------
// API Calls
// ---------------------------------------------------------------------------
async function fetchRoute() {
    if (!userLatLng || !destLatLng) return;

    showLoading(weatherCondition === 'monsoon' ? 'Computing safest monsoon route...' : 'Computing safest route...');

    try {
        const params = new URLSearchParams({
            start_lat: userLatLng[0],
            start_lon: userLatLng[1],
            end_lat: destLatLng[0],
            end_lon: destLatLng[1],
            weather_condition: weatherCondition,
        });

        const res = await fetch(`${API_BASE}/get-route?${params}`);
        if (!res.ok) {
            const err = await res.json();
            throw new Error(err.detail || 'Route failed');
        }

        const data = await res.json();

        clearRoute();
        drawRoute(data.route);

        // Update info panel
        routeInfo.style.display = 'block';
        routeDistance.textContent = `${data.distance_km} km`;
        routeNodes.textContent = data.node_count;
        floodStatusEl.textContent = data.flood_active ? '⚠️ Active' : '✅ Clear';
        floodStatusEl.className = `info-value ${data.flood_active ? 'flood-active' : ''}`;

        updateStatus(
            data.flood_active
                ? 'Rerouted — avoiding flooded roads'
                : weatherCondition === 'monsoon'
                    ? 'Monsoon safe route calculated'
                    : 'Safe route calculated',
            data.flood_active ? 'warning' : 'ok'
        );
    } catch (err) {
        updateStatus(`Error: ${err.message}`, 'danger');
        console.error('Route error:', err);
    } finally {
        hideLoading();
    }
}

async function triggerFlood() {
    showLoading('Simulating monsoon surge...');

    try {
        const res = await fetch(`${API_BASE}/simulate-flood`, { method: 'POST' });
        if (!res.ok) throw new Error('Flood simulation failed');

        const data = await res.json();
        isFloodActive = true;

        drawFloodZones(data.geometries);

        updateStatus(`🌊 ${data.flooded_roads} roads flooded!`, 'danger');
        floodStatusEl.textContent = '⚠️ Active';
        floodStatusEl.className = 'info-value flood-active';

        // Re-route to avoid flooded roads if a destination exists
        if (destLatLng) {
            loadingText.textContent = 'Re-routing to avoid floods...';
            await fetchRoute();
        }
    } catch (err) {
        updateStatus(`Error: ${err.message}`, 'danger');
        console.error('Flood error:', err);
    } finally {
        hideLoading();
    }
}

async function clearWeather() {
    showLoading('Clearing flood conditions...');

    try {
        const res = await fetch(`${API_BASE}/reset-flood`, { method: 'POST' });
        if (!res.ok) throw new Error('Reset failed');

        isFloodActive = false;
        clearFloodZones();

        updateStatus('☀️ Weather cleared — roads restored', 'ok');
        floodStatusEl.textContent = '✅ Clear';
        floodStatusEl.className = 'info-value';

        // Re-route on clear roads if a destination exists
        if (destLatLng) {
            loadingText.textContent = 'Recalculating optimal route...';
            await fetchRoute();
        }
    } catch (err) {
        updateStatus(`Error: ${err.message}`, 'danger');
        console.error('Reset error:', err);
    } finally {
        hideLoading();
    }
}

// ---------------------------------------------------------------------------
// Event Listeners
// ---------------------------------------------------------------------------
btnRoute.addEventListener('click', fetchRoute);
btnFlood.addEventListener('click', triggerFlood);
btnClear.addEventListener('click', clearWeather);
if (btnClearRoute) btnClearRoute.addEventListener('click', clearSelection);
if (btnSearchStart) btnSearchStart.addEventListener('click', () => searchLocation('start'));
if (btnSearchEnd) btnSearchEnd.addEventListener('click', () => searchLocation('end'));
if (inputStart) inputStart.addEventListener('keydown', (e) => e.key === 'Enter' && searchLocation('start'));
if (inputEnd) inputEnd.addEventListener('keydown', (e) => e.key === 'Enter' && searchLocation('end'));

// GPS Button snap trigger
if (btnGps) {
    btnGps.addEventListener('click', handleGpsSnapping);
}

// Settings changes
if (toggleMonsoon) {
    toggleMonsoon.addEventListener('change', async (e) => {
        weatherCondition = e.target.checked ? 'monsoon' : 'clear';
        updateStatus(`Monsoon simulation: ${weatherCondition === 'monsoon' ? 'ON' : 'OFF'}`, 'ok');
        
        // Auto trigger route calculation if locations exist
        if (userLatLng && destLatLng) {
            await fetchRoute();
        }
    });
}

if (toggleTheme) {
    toggleTheme.addEventListener('change', (e) => {
        const theme = e.target.checked ? 'light' : 'dark';
        document.documentElement.setAttribute('data-theme', theme);
        setTileLayer(theme);
        updateStatus(`Theme: ${theme.toUpperCase()}`, 'ok');
    });
}

// ---------------------------------------------------------------------------
// Health Check & Init
// ---------------------------------------------------------------------------
async function init() {
    updateStatus('Connecting to server...', 'warning');
    initRippleEffect();

    try {
        const res = await fetch(`${API_BASE}/health`);
        if (!res.ok) throw new Error('Server unhealthy');

        const data = await res.json();
        updateStatus(
            `Engine ready — ${data.graph.nodes.toLocaleString()} nodes`,
            'ok'
        );
    } catch (err) {
        updateStatus('Server offline — start backend first', 'danger');
        console.error('Health check failed:', err);
    }

    initGeolocation();
}

init();
