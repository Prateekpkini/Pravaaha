"""
app.py — Pravaaha: Flood-Aware Evacuation Routing Engine

FastAPI backend that loads Mangalore's street graph into memory at startup
and provides sub-second routing with synthetic flood simulation.
"""

import os
import random
import math
from contextlib import asynccontextmanager
from typing import Optional

import networkx as nx
import osmnx as ox
from scipy.spatial import KDTree
from fastapi import FastAPI, HTTPException, Query
from fastapi.staticfiles import StaticFiles
from fastapi.responses import FileResponse

# ---------------------------------------------------------------------------
# Global state
# ---------------------------------------------------------------------------
GRAPH_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "mangalore.graphml")

graph: Optional[nx.MultiDiGraph] = None
kdtree: Optional[KDTree] = None
node_ids: list = []
# Track flooded edges: list of (u, v, key, original_travel_time)
flooded_edges: list[tuple] = []
# Store flooded road geometries for frontend rendering
flooded_geometries: list[list[list[float]]] = []


# ---------------------------------------------------------------------------
# Lifespan — load graph once at startup
# ---------------------------------------------------------------------------
@asynccontextmanager
async def lifespan(app: FastAPI):
    global graph, kdtree, node_ids
    print("Loading Mangalore street graph into memory...")
    if not os.path.exists(GRAPH_PATH):
        raise FileNotFoundError(
            f"Graph file not found: {GRAPH_PATH}\n"
            "Run `python map_builder.py` first to download the map."
        )
    graph = ox.load_graphml(GRAPH_PATH)
    print(f"Graph loaded -- {graph.number_of_nodes():,} nodes, {graph.number_of_edges():,} edges")
    
    print("Preprocessing edge weights and building spatial index...")
    # Preprocess edge weights (travel_time)
    for u, v, k, d in graph.edges(keys=True, data=True):
        length_m = d.get("length", 100)
        speed_val = d.get("maxspeed", 30)
        if isinstance(speed_val, list): speed_val = speed_val[0]
        try:
            speed_kmh = float(speed_val)
        except:
            hw = d.get("highway", "")
            speeds = {
                "motorway": 100, "trunk": 80, "primary": 60, "secondary": 50,
                "tertiary": 40, "unclassified": 30, "residential": 30
            }
            if isinstance(hw, list): hw = hw[0]
            speed_kmh = speeds.get(hw, 30)
            
        speed_ms = speed_kmh * 1000 / 3600
        d["travel_time"] = length_m / speed_ms

    # Build KD-Tree for fast node snapping
    nodes = list(graph.nodes(data=True))
    node_ids = [n[0] for n in nodes]
    coords = [[n[1]['x'], n[1]['y']] for n in nodes]
    kdtree = KDTree(coords)

    print("Backend ready.")
    yield
    graph = None
    kdtree = None
    node_ids = []
    print("Graph unloaded.")


# ---------------------------------------------------------------------------
# App
# ---------------------------------------------------------------------------
app = FastAPI(
    title="Pravaaha — Flood-Aware Evacuation Router",
    version="1.0.0",
    lifespan=lifespan,
)


# ---------------------------------------------------------------------------
# API Endpoints
# ---------------------------------------------------------------------------

@app.get("/health")
async def health():
    """Health check — returns graph stats to confirm the engine is live."""
    if graph is None:
        raise HTTPException(status_code=503, detail="Graph not loaded")
    return {
        "status": "ok",
        "graph": {
            "nodes": graph.number_of_nodes(),
            "edges": graph.number_of_edges(),
        },
        "flood_active": len(flooded_edges) > 0,
        "flooded_road_count": len(flooded_edges),
    }


def haversine_heuristic(u, v):
    node1 = graph.nodes[u]
    node2 = graph.nodes[v]
    lon1, lat1 = node1['x'], node1['y']
    lon2, lat2 = node2['x'], node2['y']
    lat1, lon1, lat2, lon2 = map(math.radians, [lat1, lon1, lat2, lon2])
    dlat = lat2 - lat1
    dlon = lon2 - lon1
    a = math.sin(dlat/2)**2 + math.cos(lat1) * math.cos(lat2) * math.sin(dlon/2)**2
    c = 2 * math.asin(math.sqrt(a))
    distance_m = c * 6371000
    # Return optimistic travel time (assuming max network speed of 100 km/h = 27.78 m/s)
    return distance_m / 27.78


@app.get("/get-route")
async def get_route(
    start_lat: float = Query(..., description="Start latitude"),
    start_lon: float = Query(..., description="Start longitude"),
    end_lat: float = Query(..., description="End latitude"),
    end_lon: float = Query(..., description="End longitude"),
):
    """
    Find the fastest driveable route between two coordinates using A*.
    Respects dynamically modified flood weights for real-time rerouting.
    """
    if graph is None or kdtree is None:
        raise HTTPException(status_code=503, detail="Graph not loaded")

    try:
        # Snap lat/lon to nearest graph nodes using KD-Tree
        _, idx1 = kdtree.query([start_lon, start_lat])
        orig_node = node_ids[idx1]
        _, idx2 = kdtree.query([end_lon, end_lat])
        dest_node = node_ids[idx2]
    except Exception as e:
        raise HTTPException(status_code=400, detail=f"Could not snap coordinates to graph: {e}")

    if orig_node == dest_node:
        raise HTTPException(status_code=400, detail="Start and end resolve to the same node")

    try:
        # A* shortest path using 'travel_time' attribute
        path_nodes = nx.astar_path(graph, orig_node, dest_node, heuristic=haversine_heuristic, weight="travel_time")
    except nx.NetworkXNoPath:
        raise HTTPException(status_code=404, detail="No route found between these points")

    # Convert node IDs to [lat, lon] coordinates
    coords = []
    total_length = 0.0
    for i, node in enumerate(path_nodes):
        node_data = graph.nodes[node]
        coords.append([node_data["y"], node_data["x"]])  # [lat, lon]
        if i > 0:
            # Sum the edge lengths for total distance
            edge_data = graph.get_edge_data(path_nodes[i - 1], node)
            if edge_data:
                # MultiDiGraph: pick shortest edge between pair
                min_len = min(d.get("length", 0) for d in edge_data.values())
                total_length += min_len

    return {
        "route": coords,
        "distance_m": round(total_length, 1),
        "distance_km": round(total_length / 1000, 2),
        "node_count": len(path_nodes),
        "flood_active": len(flooded_edges) > 0,
    }


@app.post("/simulate-flood")
async def simulate_flood():
    """
    Synthetic Flood Simulator — randomly select 5-10 road segments
    and increase their traversal weight by 1000x to simulate waterlogging.
    Returns the flooded road geometries for frontend hazard rendering.
    """
    global flooded_edges, flooded_geometries

    if graph is None:
        raise HTTPException(status_code=503, detail="Graph not loaded")

    # Reset any existing flood first
    _reset_flood_internal()

    # Pick 5-10 random edges to flood
    all_edges = list(graph.edges(keys=True, data=True))
    num_to_flood = random.randint(5, 10)
    candidates = random.sample(all_edges, min(num_to_flood, len(all_edges)))

    flooded_edges = []
    flooded_geometries = []

    for u, v, key, data in candidates:
        original_tt = data.get("travel_time", 10.0)

        # Store original weight for reset
        flooded_edges.append((u, v, key, original_tt))

        # Multiply weight by 1000x to make this road extremely costly
        graph[u][v][key]["travel_time"] = original_tt * 1000

        # Build geometry for frontend rendering
        u_data = graph.nodes[u]
        v_data = graph.nodes[v]
        segment = [
            [u_data["y"], u_data["x"]],  # [lat, lon]
            [v_data["y"], v_data["x"]],
        ]

        # If edge has detailed geometry, use it instead
        if "geometry" in data:
            try:
                geom_coords = list(data["geometry"].coords)
                segment = [[lat, lon] for lon, lat in geom_coords]
            except Exception:
                pass  # Fall back to simple u→v segment

        flooded_geometries.append(segment)

    return {
        "status": "flood_simulated",
        "flooded_roads": len(flooded_edges),
        "geometries": flooded_geometries,
    }


def _reset_flood_internal():
    """Internal helper to restore all flooded edge weights."""
    global flooded_edges, flooded_geometries
    for u, v, key, original_tt in flooded_edges:
        try:
            graph[u][v][key]["travel_time"] = original_tt
        except KeyError:
            pass  # Edge may have been removed
    flooded_edges = []
    flooded_geometries = []


@app.post("/reset-flood")
async def reset_flood():
    """Clear all simulated flood conditions and restore original road weights."""
    if graph is None:
        raise HTTPException(status_code=503, detail="Graph not loaded")

    restored_count = len(flooded_edges)
    _reset_flood_internal()

    return {
        "status": "flood_cleared",
        "roads_restored": restored_count,
    }


# ---------------------------------------------------------------------------
# Serve frontend static files
# ---------------------------------------------------------------------------
STATIC_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "static")
if os.path.isdir(STATIC_DIR):
    app.mount("/static", StaticFiles(directory=STATIC_DIR), name="static")


@app.get("/")
async def root():
    """Serve the main frontend page."""
    index_path = os.path.join(STATIC_DIR, "index.html")
    if os.path.exists(index_path):
        return FileResponse(index_path)
    return {"message": "Pravaaha API is running. No frontend found in /static."}
