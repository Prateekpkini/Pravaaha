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
    weather_condition: str = Query("clear", description="Weather condition (clear or monsoon)"),
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
        if weather_condition == "monsoon":
            def monsoon_weight_fn(u, v, edge_dict):
                min_w = float('inf')
                for key, data in edge_dict.items():
                    original_tt = data.get("travel_time", 10.0)
                    
                    # Snap node coordinates to compute elevation
                    u_node = graph.nodes[u]
                    v_node = graph.nodes[v]
                    avg_lat = (u_node["y"] + v_node["y"]) / 2.0
                    avg_lon = (u_node["x"] + v_node["x"]) / 2.0
                    
                    # Proximity to Netravati River (South)
                    dist_netravati = abs(avg_lat - 12.835)
                    # Proximity to Gurupura River (North/West)
                    dist_gurupura_ns = abs(avg_lon - 74.825) if 12.85 <= avg_lat <= 12.92 else 999.0
                    dist_gurupura_ew = abs(avg_lat - 12.925) if 74.82 <= avg_lon <= 74.88 else 999.0
                    # Proximity to Arabian Sea (West)
                    dist_sea = abs(avg_lon - 74.815)
                    
                    min_dist_to_water = min(dist_netravati, dist_gurupura_ns, dist_gurupura_ew, dist_sea)
                    dist_meters = min_dist_to_water * 111000
                    
                    # Synthetic elevation (m)
                    elevation = 2.0 + 48.0 * (1.0 - math.exp(-dist_meters / 1000.0))
                    
                    # Low-lying roads penalty: exponential penalty for low elevations
                    low_lying_penalty = 1.0 + 15.0 * math.exp(-elevation / 8.0)
                    
                    # Dirt path penalty: identify unpaved/minor tracks
                    highway = data.get("highway", "")
                    if isinstance(highway, list):
                        highway = highway[0]
                    
                    dirt_penalty = 1.0
                    if highway in ["track", "path", "unclassified", "service"]:
                        dirt_penalty = 8.0
                        
                    weight = original_tt * low_lying_penalty * dirt_penalty
                    if weight < min_w:
                        min_w = weight
                return min_w
                
            path_nodes = nx.astar_path(graph, orig_node, dest_node, heuristic=haversine_heuristic, weight=monsoon_weight_fn)
        else:
            path_nodes = nx.astar_path(graph, orig_node, dest_node, heuristic=haversine_heuristic, weight="travel_time")
    except nx.NetworkXNoPath:
        raise HTTPException(status_code=404, detail="No route found between these points")

    # Convert node IDs to [lat, lon] coordinates
    coords = []
    total_length = 0.0
    total_time = 0.0
    for i, node in enumerate(path_nodes):
        node_data = graph.nodes[node]
        coords.append([node_data["y"], node_data["x"]])  # [lat, lon]
        if i > 0:
            # Sum the edge lengths for total distance
            edge_data = graph.get_edge_data(path_nodes[i - 1], node)
            if edge_data:
                # MultiDiGraph: pick shortest edge between pair
                min_len = min(d.get("length", 0) for d in edge_data.values())
                min_time = min(d.get("travel_time", 10.0) for d in edge_data.values())
                total_length += min_len
                total_time += min_time

    return {
        "route": coords,
        "distance_m": round(total_length, 1),
        "distance_km": round(total_length / 1000, 2),
        "estimated_time_s": round(total_time, 1),
        "node_count": len(path_nodes),
        "flood_active": len(flooded_edges) > 0,
    }


@app.get("/get-flood-route")
async def get_flood_route(
    start_lat: float = Query(..., description="Ambulance current latitude"),
    start_lon: float = Query(..., description="Ambulance current longitude"),
    end_lat: float = Query(..., description="Destination latitude"),
    end_lon: float = Query(..., description="Destination longitude"),
    flood_lat: float = Query(..., description="Flood zone center latitude"),
    flood_lon: float = Query(..., description="Flood zone center longitude"),
    flood_radius_m: float = Query(..., description="Flood zone radius in meters"),
):
    """
    Compute a route that avoids an expanding circular flood zone.
    Temporarily penalizes all edges touching nodes inside the flood radius,
    then restores original weights after pathfinding.
    """
    if graph is None or kdtree is None:
        raise HTTPException(status_code=503, detail="Graph not loaded")

    try:
        _, idx1 = kdtree.query([start_lon, start_lat])
        orig_node = node_ids[idx1]
        _, idx2 = kdtree.query([end_lon, end_lat])
        dest_node = node_ids[idx2]
    except Exception as e:
        raise HTTPException(status_code=400, detail=f"Could not snap coordinates to graph: {e}")

    if orig_node == dest_node:
        raise HTTPException(status_code=400, detail="Start and end resolve to the same node")

    # Convert flood radius from meters to approximate degrees for KD-Tree query
    # 1 degree latitude ~ 111,000m; 1 degree longitude ~ 111,000m * cos(lat)
    radius_deg = flood_radius_m / 111000.0

    # Find all node indices within the flood radius
    flooded_indices = kdtree.query_ball_point([flood_lon, flood_lat], radius_deg)

    # Collect the flooded node IDs
    flooded_node_set = set()
    for idx in flooded_indices:
        flooded_node_set.add(node_ids[idx])

    # Temporarily penalize all edges touching flooded nodes
    modified_edges = []
    PENALTY = 999999.0
    for fnode in flooded_node_set:
        # Outgoing edges
        if graph.has_node(fnode):
            for u, v, k, d in graph.edges(fnode, keys=True, data=True):
                original_tt = d.get("travel_time", 10.0)
                if original_tt < PENALTY:
                    modified_edges.append((u, v, k, original_tt))
                    d["travel_time"] = PENALTY
            # Incoming edges (for DiGraph traversal)
            for u, v, k, d in graph.in_edges(fnode, keys=True, data=True):
                original_tt = d.get("travel_time", 10.0)
                if original_tt < PENALTY:
                    modified_edges.append((u, v, k, original_tt))
                    d["travel_time"] = PENALTY

    try:
        path_nodes = nx.astar_path(
            graph, orig_node, dest_node,
            heuristic=haversine_heuristic, weight="travel_time"
        )
    except nx.NetworkXNoPath:
        # Restore weights before raising
        for u, v, k, orig_tt in modified_edges:
            try:
                graph[u][v][k]["travel_time"] = orig_tt
            except KeyError:
                pass
        raise HTTPException(status_code=404, detail="No route found avoiding the flood zone")
    finally:
        # Always restore original weights
        for u, v, k, orig_tt in modified_edges:
            try:
                graph[u][v][k]["travel_time"] = orig_tt
            except KeyError:
                pass

    # Build response coordinates and distance
    coords = []
    total_length = 0.0
    total_time = 0.0
    for i, node in enumerate(path_nodes):
        node_data = graph.nodes[node]
        coords.append([node_data["y"], node_data["x"]])
        if i > 0:
            edge_data = graph.get_edge_data(path_nodes[i - 1], node)
            if edge_data:
                min_len = min(d.get("length", 0) for d in edge_data.values())
                min_time = min(d.get("travel_time", 10.0) for d in edge_data.values())
                total_length += min_len
                total_time += min_time

    return {
        "route": coords,
        "distance_m": round(total_length, 1),
        "distance_km": round(total_length / 1000, 2),
        "estimated_time_s": round(total_time, 1),
        "node_count": len(path_nodes),
        "flooded_nodes_avoided": len(flooded_node_set),
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
