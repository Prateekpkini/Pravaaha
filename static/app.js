/**
 * Pravaaha — app.js
 * Frontend logic: Leaflet map, geolocation, routing, flood visualization
 */

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

// CartoDB Dark Matter tiles for sleek dark aesthetic
L.tileLayer('https://{s}.basemaps.cartocdn.com/dark_all/{z}/{x}/{y}{r}.png', {
    attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OSM</a> &copy; <a href="https://carto.com/">CARTO</a>',
    subdomains: 'abcd',
    maxZoom: 19,
}).addTo(map);

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

// ---------------------------------------------------------------------------
// DOM References
// ---------------------------------------------------------------------------
const btnRoute = document.getElementById('btn-route');
const btnFlood = document.getElementById('btn-flood');
const btnClear = document.getElementById('btn-clear');
const statusDot = document.getElementById('status-dot');
const statusText = document.getElementById('status-text');
const routeInfo = document.getElementById('route-info');
const routeDistance = document.getElementById('route-distance');
const routeNodes = document.getElementById('route-nodes');
const floodStatusEl = document.getElementById('flood-status');
const loadingOverlay = document.getElementById('loading-overlay');
const loadingText = document.getElementById('loading-text');

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
// Geolocation
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
    userLatLng = [lat, lon];

    if (userMarker) {
        userMarker.setLatLng(userLatLng);
    } else {
        userMarker = L.marker(userLatLng, { icon: createUserIcon() })
            .addTo(map)
            .bindPopup('<b>📍 Your Location</b>');
    }

    map.setView(userLatLng, DEFAULT_ZOOM);
}

// ---------------------------------------------------------------------------
// Destination Selection
// ---------------------------------------------------------------------------
map.on('click', (e) => {
    destLatLng = [e.latlng.lat, e.latlng.lng];

    if (destMarker) {
        destMarker.setLatLng(destLatLng);
    } else {
        destMarker = L.marker(destLatLng, { icon: createDestIcon() })
            .addTo(map)
            .bindPopup('<b>🎯 Destination</b>');
    }

    destMarker.openPopup();

    // Enable the route button
    btnRoute.disabled = false;
    updateStatus('Destination set — ready to route', 'ok');
});

// ---------------------------------------------------------------------------
// Ripple effect on buttons
// ---------------------------------------------------------------------------
document.querySelectorAll('.btn').forEach((btn) => {
    btn.addEventListener('mousemove', (e) => {
        const rect = btn.getBoundingClientRect();
        const x = ((e.clientX - rect.left) / rect.width) * 100;
        const y = ((e.clientY - rect.top) / rect.height) * 100;
        btn.style.setProperty('--x', x + '%');
        btn.style.setProperty('--y', y + '%');
    });
});

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

    showLoading('Computing safest route...');

    try {
        const params = new URLSearchParams({
            start_lat: userLatLng[0],
            start_lon: userLatLng[1],
            end_lat: destLatLng[0],
            end_lon: destLatLng[1],
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

// ---------------------------------------------------------------------------
// Health Check & Init
// ---------------------------------------------------------------------------
async function init() {
    updateStatus('Connecting to server...', 'warning');

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
