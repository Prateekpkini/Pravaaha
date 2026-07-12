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
const KANACHUR_CENTER = [12.8105, 74.8732];
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
let isFloodActive = false;

// Simulation State
let isSimulating = false;
let animationFrameId = null;
let currentPathIndex = 0;
let currentPathFraction = 0;
let currentRouteCoords = [];
let currentDirections = [];
let currentDirectionIndex = 0;
const FLOOD_INITIAL_RADIUS = 50;
const FLOOD_MAX_RADIUS = 400;
const FLOOD_GROWTH_RATE = 30; // meters per second
const FLOOD_GROWTH_DURATION = 6; // seconds until growth freezes

let floodCircle = null;
let floodCenter = null;
let floodRadius = 0;
let floodGrowthTime = 0;
let floodGrowthFrozen = false;
let lastFrameTime = 0;
let currentSegmentDistance = null;
let lastCollisionCheckTime = 0;
let lastMapPanTime = 0;

// ---------------------------------------------------------------------------
// DOM References
// ---------------------------------------------------------------------------
const btnSimulate = document.getElementById('btn-simulate');
const btnClearRoute = document.getElementById('btn-clear-route');
const inputEnd = document.getElementById('input-end');
const btnSearchEnd = document.getElementById('btn-search-end');
const statusDot = document.getElementById('status-dot');
const statusText = document.getElementById('status-text');
const routeInfo = document.getElementById('route-info');
const routeDistance = document.getElementById('route-distance');
const routeEta = document.getElementById('route-eta');
const floodStatusEl = document.getElementById('flood-status');
const directionsSection = document.getElementById('directions-section');
const nextDirectionText = document.getElementById('next-direction');
const directionsListEl = document.getElementById('directions-list');
const loadingOverlay = document.getElementById('loading-overlay');
const loadingText = document.getElementById('loading-text');

// Toggles
const toggleTheme = document.getElementById('toggle-theme');

// ---------------------------------------------------------------------------
// Custom Markers
// ---------------------------------------------------------------------------
function createAmbulanceIcon() {
    return L.divIcon({
        className: 'ambulance-marker',
        html: `<div class="ambulance-marker-pulse"></div>🚑`,
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
    // Hardcode to Kanachur IMS for the simulation
    setStartMarker(KANACHUR_CENTER[0], KANACHUR_CENTER[1]);
    map.setView(KANACHUR_CENTER, 14);
    updateStatus('Ambulance stationed at Kanachur IMS. Click map for destination.', 'ok');
}

function setStartMarker(lat, lon) {
    userLatLng = [lat, lon];
    if (userMarker) {
        userMarker.setLatLng(userLatLng);
    } else {
        userMarker = L.marker(userLatLng, { icon: createAmbulanceIcon(), zIndexOffset: 1000 }).addTo(map).bindPopup('<b>🚑 Kanachur IMS</b>');
    }
}

// ---------------------------------------------------------------------------
// Destination Selection
// ---------------------------------------------------------------------------
map.on('click', (e) => {
    if (isSimulating) return; // Prevent changing destination during sim
    setEndMarker(e.latlng.lat, e.latlng.lng);
    updateStatus('Destination set — fetching route...', 'ok');
    fetchRoute();
});

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
        btnSimulate.disabled = false;
    } else {
        btnSimulate.disabled = true;
    }
}

async function searchLocation() {
    const query = inputEnd.value.trim();
    if (!query) return;

    showLoading(`Searching for destination...`);
    try {
        const res = await fetch(`https://nominatim.openstreetmap.org/search?format=json&q=${encodeURIComponent(query)}&limit=1`);
        const data = await res.json();
        if (data && data.length > 0) {
            const lat = parseFloat(data[0].lat);
            const lon = parseFloat(data[0].lon);
            setEndMarker(lat, lon);
            map.setView([lat, lon], DEFAULT_ZOOM);
            updateStatus('Destination found — fetching route...', 'ok');
            fetchRoute();
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
    if (isSimulating) {
        cancelAnimationFrame(animationFrameId);
        isSimulating = false;
    }

    if (destMarker) {
        map.removeLayer(destMarker);
        destMarker = null;
    }
    if (floodCircle) {
        map.removeLayer(floodCircle);
        floodCircle = null;
    }

    // Reset ambulance to start
    setStartMarker(KANACHUR_CENTER[0], KANACHUR_CENTER[1]);

    destLatLng = null;
    if (inputEnd) inputEnd.value = '';
    clearRoute();
    routeInfo.style.display = 'none';
    checkRouteReady();
    updateStatus('Selection cleared. Set a new destination.', 'ok');
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

    // Fit map to route with extra left padding for the sidebar
    map.fitBounds(routePolyline.getBounds(), {
        padding: [80, 80],
        paddingTopLeft: [300, 20],
    });
    lastMapPanTime = performance.now();
}

function hideDirections() {
    if (directionsSection) {
        directionsSection.style.display = 'none';
    }
    if (directionsListEl) {
        directionsListEl.innerHTML = '';
    }
    if (nextDirectionText) {
        nextDirectionText.textContent = '—';
    }
    currentDirections = [];
    currentDirectionIndex = 0;
}

function getBearing([lat1, lon1], [lat2, lon2]) {
    const rad = Math.PI / 180;
    const phi1 = lat1 * rad;
    const phi2 = lat2 * rad;
    const deltaLambda = (lon2 - lon1) * rad;
    const y = Math.sin(deltaLambda) * Math.cos(phi2);
    const x = Math.cos(phi1) * Math.sin(phi2) - Math.sin(phi1) * Math.cos(phi2) * Math.cos(deltaLambda);
    const theta = Math.atan2(y, x);
    return (theta * 180 / Math.PI + 360) % 360;
}

function getTurnInstruction(prevBearing, nextBearing) {
    let diff = nextBearing - prevBearing;
    if (diff > 180) diff -= 360;
    if (diff < -180) diff += 360;

    if (Math.abs(diff) <= 25) {
        return 'Go straight';
    }
    if (diff > 25) {
        return 'Turn right';
    }
    return 'Turn left';
}

function buildDirections(route) {
    if (!route || route.length < 2) {
        return [{ routeIndex: 0, text: 'Start and head to the destination' }];
    }

    const directions = [];
    let prevBearing = getBearing(route[0], route[1]);
    directions.push({ routeIndex: 0, text: 'Start by moving straight' });

    for (let i = 1; i < route.length - 1; i += 1) {
        const bearing = getBearing(route[i], route[i + 1]);
        const instruction = getTurnInstruction(prevBearing, bearing);
        if (instruction !== 'Go straight') {
            directions.push({ routeIndex: i, text: `${instruction} ahead` });
            prevBearing = bearing;
        }
    }

    directions.push({ routeIndex: route.length - 1, text: 'Arrive at your destination' });
    return directions;
}

function renderDirections() {
    if (!directionsSection || !directionsListEl || !nextDirectionText) return;

    if (currentDirections.length === 0) {
        hideDirections();
        return;
    }

    directionsSection.style.display = 'block';
    directionsListEl.innerHTML = currentDirections.map((direction, index) => {
        const activeClass = index === currentDirectionIndex ? 'active' : '';
        return `<li class="${activeClass}">${direction.text}</li>`;
    }).join('');
    nextDirectionText.textContent = currentDirections[currentDirectionIndex]?.text || '—';
}

function updateCurrentDirection() {
    if (!currentDirections || currentDirections.length === 0) return;

    while (currentDirectionIndex < currentDirections.length - 1 &&
        currentPathIndex >= currentDirections[currentDirectionIndex + 1].routeIndex) {
        currentDirectionIndex += 1;
    }

    renderDirections();
}

function clearRoute() {
    if (routePolyline) {
        if (routePolyline._glowLine) {
            map.removeLayer(routePolyline._glowLine);
        }
        map.removeLayer(routePolyline);
        routePolyline = null;
    }
    hideDirections();
}

// ---------------------------------------------------------------------------
// API Calls & Simulation
// ---------------------------------------------------------------------------
async function fetchRoute() {
    if (!userLatLng || !destLatLng) return;

    showLoading('Computing optimal route...');

    try {
        const params = new URLSearchParams({
            start_lat: userLatLng[0],
            start_lon: userLatLng[1],
            end_lat: destLatLng[0],
            end_lon: destLatLng[1],
            weather_condition: 'clear',
        });

        const res = await fetch(`${API_BASE}/get-route?${params}`);
        if (!res.ok) {
            const err = await res.json();
            throw new Error(err.detail || 'Route failed');
        }

        const data = await res.json();

        clearRoute();
        drawRoute(data.route);
        currentRouteCoords = data.route;
        currentPathIndex = 0;
        currentPathFraction = 0;
        currentSegmentDistance = null;
        currentDirections = buildDirections(data.route);
        currentDirectionIndex = 0;
        renderDirections();

        // Update info panel
        routeInfo.style.display = 'block';
        directionsSection.style.display = 'block';
        routeDistance.textContent = `${data.distance_km} km`;
        routeEta.textContent = `${Math.ceil(data.estimated_time_s / 60)} min`;
        floodStatusEl.textContent = 'Clear';
        floodStatusEl.className = `info-value`;

        updateStatus('Route calculated. Ready for simulation.', 'ok');
    } catch (err) {
        updateStatus(`Error: ${err.message}`, 'danger');
        console.error('Route error:', err);
    } finally {
        hideLoading();
    }
}

// Start the simulation loop
function handleStartSimulation() {
    if (!currentRouteCoords || currentRouteCoords.length === 0) return;

    isSimulating = true;
    btnSimulate.disabled = true;
    updateStatus('Simulation running...', 'warning');

    // Pick a flood location slightly ahead on the route
    if (!floodCircle) {
        const midIndex = Math.min(currentRouteCoords.length - 1, Math.floor(currentRouteCoords.length * 0.4));
        const targetPoint = currentRouteCoords[midIndex];
        // Offset it slightly so it has to grow to hit the route
        floodCenter = [targetPoint[0] + 0.002, targetPoint[1] + 0.002];
        floodRadius = FLOOD_INITIAL_RADIUS;
        floodGrowthTime = 0;
        floodGrowthFrozen = false;

        floodCircle = L.circle(floodCenter, {
            color: '#ef4444',
            fillColor: '#ef4444',
            fillOpacity: 0.4,
            radius: floodRadius,
            weight: 2
        }).addTo(map);
    }

    lastFrameTime = performance.now();
    animationFrameId = requestAnimationFrame(simulationLoop);
}

async function simulationLoop(time) {
    if (!isSimulating) return;

    // Calculate elapsed time (delta time) in seconds
    const deltaSeconds = Math.min((time - lastFrameTime) / 1000, 0.1); // cap delta time to 100ms to avoid huge jumps
    lastFrameTime = time;

    // Growth rate: 30 meters per second until frozen
    if (!floodGrowthFrozen) {
        floodGrowthTime += deltaSeconds;
        floodRadius += FLOOD_GROWTH_RATE * deltaSeconds;
        if (floodGrowthTime >= FLOOD_GROWTH_DURATION || floodRadius >= FLOOD_MAX_RADIUS) {
            floodRadius = Math.min(floodRadius, FLOOD_MAX_RADIUS);
            floodGrowthFrozen = true;
        }
    }
    if (floodCircle) {
        floodCircle.setRadius(floodRadius);
    }

    // Move ambulance
    if (currentPathIndex < currentRouteCoords.length - 1) {
        const p1 = currentRouteCoords[currentPathIndex];
        const p2 = currentRouteCoords[currentPathIndex + 1];

        // Cache segment distance to avoid costly map.distance recalculations every frame
        if (currentSegmentDistance === null) {
            currentSegmentDistance = map.distance(p1, p2);
        }

        // Ambulance speed: 120 meters per second for smooth, fast simulation
        const moveDist = 120 * deltaSeconds;

        if (currentSegmentDistance > 0) {
            currentPathFraction += moveDist / currentSegmentDistance;
        } else {
            currentPathFraction = 1;
        }

        if (currentPathFraction >= 1) {
            currentPathIndex++;
            currentPathFraction = 0;
            currentSegmentDistance = null; // Reset segment distance cache for the next segment
            // Snap exactly to next point on reaching it
            if (currentPathIndex < currentRouteCoords.length) {
                userLatLng = currentRouteCoords[currentPathIndex];
                if (userMarker) userMarker.setLatLng(userLatLng);
            }
            updateCurrentDirection();
        } else {
            const lat = p1[0] + (p2[0] - p1[0]) * currentPathFraction;
            const lng = p1[1] + (p2[1] - p1[1]) * currentPathFraction;
            userLatLng = [lat, lng];
            if (userMarker) {
                userMarker.setLatLng(userLatLng);
            }
        }

        // Keep the ambulance visible by periodically panning the map slightly right of center
        if (time - lastMapPanTime >= 400) {
            map.panTo(userLatLng, { animate: false });
            map.panBy([120, 0], { animate: false });
            lastMapPanTime = time;
        }
    } else {
        // reached destination
        isSimulating = false;
        btnSimulate.disabled = false;
        updateStatus('Destination Reached!', 'ok');
        return;
    }

    // Collision Detection with Turf.js: Throttle checks to every 250ms for performance optimization
    if (floodCircle && (time - lastCollisionCheckTime >= 250)) {
        lastCollisionCheckTime = time;
        // Build remaining path
        const remainingCoords = [userLatLng, ...currentRouteCoords.slice(currentPathIndex + 1)];
        if (remainingCoords.length >= 2) {
            // Turf uses [lon, lat]
            const line = turf.lineString(remainingCoords.map(c => [c[1], c[0]]));
            const center = turf.point([floodCenter[1], floodCenter[0]]);
            const circle = turf.circle(center, floodRadius / 1000, { steps: 32, units: 'kilometers' });

            if (turf.booleanIntersects(line, circle)) {
                isSimulating = false; // Pause
                updateStatus('Flood collision imminent! Rerouting...', 'danger');
                await fetchFloodRoute();
                return; // fetchFloodRoute will resume loop
            }
        }
    }

    animationFrameId = requestAnimationFrame(simulationLoop);
}

async function fetchFloodRoute() {
    try {
        const params = new URLSearchParams({
            start_lat: userLatLng[0],
            start_lon: userLatLng[1],
            end_lat: destLatLng[0],
            end_lon: destLatLng[1],
            flood_lat: floodCenter[0],
            flood_lon: floodCenter[1],
            flood_radius_m: floodRadius
        });

        const res = await fetch(`${API_BASE}/get-flood-route?${params}`);
        if (!res.ok) {
            throw new Error('No safe route around flood found.');
        }

        const data = await res.json();

        // Remove old glow and draw new route
        clearRoute();
        drawRoute(data.route);

        currentRouteCoords = data.route;
        currentPathIndex = 0;
        currentPathFraction = 0;
        currentSegmentDistance = null;
        currentDirections = buildDirections(data.route);
        currentDirectionIndex = 0;
        renderDirections();

        // Update info panel
        routeDistance.textContent = `${data.distance_km} km`;
        routeEta.textContent = `${Math.ceil(data.estimated_time_s / 60)} min`;
        floodStatusEl.textContent = 'Rerouted';
        floodStatusEl.className = 'info-value flood-active';
        updateStatus('Rerouted successfully. Resuming...', 'warning');

        // Give it a brief pause for visual effect, then resume
        setTimeout(() => {
            if (destLatLng) {
                isSimulating = true;
                lastFrameTime = performance.now();
                animationFrameId = requestAnimationFrame(simulationLoop);
            }
        }, 1000);

    } catch (err) {
        updateStatus(`Reroute Error: ${err.message}`, 'danger');
        console.error('Reroute error:', err);
    }
}

// ---------------------------------------------------------------------------
// Event Listeners
// ---------------------------------------------------------------------------
btnSimulate.addEventListener('click', handleStartSimulation);
if (btnClearRoute) btnClearRoute.addEventListener('click', clearSelection);
if (btnSearchEnd) btnSearchEnd.addEventListener('click', searchLocation);
if (inputEnd) inputEnd.addEventListener('keydown', (e) => e.key === 'Enter' && searchLocation());

// Settings changes
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
