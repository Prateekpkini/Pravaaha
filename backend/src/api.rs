use axum::{
    extract::{State, Query},
    routing::{get, post},
    Json, Router,
};
use serde::{Deserialize, Serialize};
use std::sync::{Arc, RwLock};
use crate::graph::{RoutingGraph, EdgeId};
use crate::routing::{bidirectional_astar, isochrone};
use rstar::PointDistance;

#[derive(Deserialize)]
pub struct FloodUpdate {
    lat: f64,
    lng: f64,
    depth_mm: u32,
}

#[derive(Deserialize)]
pub struct RouteRequest {
    start_lat: f64,
    start_lng: f64,
    end_lat: f64,
    end_lng: f64,
    clearance_mm: u32,
}

#[derive(Deserialize)]
pub struct IsochroneRequest {
    lat: f64,
    lng: f64,
    time_limit_sec: f64,
    clearance_mm: u32,
}

#[derive(Serialize)]
pub struct GeoJsonFeature {
    #[serde(rename = "type")]
    typ: String,
    geometry: GeoJsonGeometry,
}

#[derive(Serialize)]
pub struct GeoJsonGeometry {
    #[serde(rename = "type")]
    typ: String,
    coordinates: Vec<[f64; 2]>, // [lng, lat]
}

type SharedState = Arc<RwLock<RoutingGraph>>;

pub fn create_router(state: SharedState) -> Router {
    Router::new()
        .route("/api/flood-update", post(handle_flood_update))
        .route("/api/route", get(handle_route))
        .route("/api/isochrone", get(handle_isochrone))
        .with_state(state)
}

async fn handle_flood_update(
    State(state): State<SharedState>,
    Json(updates): Json<Vec<FloodUpdate>>,
) -> Json<usize> {
    let mut graph = state.write().unwrap();
    let mut updated_count = 0;
    
    for update in updates {
        if let Some(nearest) = graph.spatial_index.nearest_neighbor(&[update.lat, update.lng]) {
            let edge_id = nearest.data;
            if let Some(edge) = graph.edges.get_mut(&edge_id) {
                edge.current_flood_depth_mm = update.depth_mm;
                updated_count += 1;
            }
        }
    }
    Json(updated_count)
}

fn find_nearest_node(graph: &RoutingGraph, lat: f64, lng: f64) -> Option<crate::graph::NodeId> {
    if let Some(nearest) = graph.spatial_index.nearest_neighbor(&[lat, lng]) {
        let edge = graph.edges.get(&nearest.data)?;
        let n1 = graph.nodes.get(&edge.source)?;
        let n2 = graph.nodes.get(&edge.target)?;
        
        let d1 = (n1.lat - lat).powi(2) + (n1.lng - lng).powi(2);
        let d2 = (n2.lat - lat).powi(2) + (n2.lng - lng).powi(2);
        
        if d1 < d2 {
            return Some(n1.id);
        } else {
            return Some(n2.id);
        }
    }
    None
}

async fn handle_route(
    State(state): State<SharedState>,
    Query(req): Query<RouteRequest>,
) -> Json<Option<GeoJsonFeature>> {
    let graph = state.read().unwrap();
    
    let start_node = find_nearest_node(&graph, req.start_lat, req.start_lng);
    let end_node = find_nearest_node(&graph, req.end_lat, req.end_lng);
    
    if let (Some(start), Some(end)) = (start_node, end_node) {
        if let Some(path) = bidirectional_astar(&graph, start, end, req.clearance_mm) {
            let mut coords = Vec::new();
            if !path.is_empty() {
                let first_edge = graph.edges.get(&path[0]).unwrap();
                let first_node = graph.nodes.get(&first_edge.source).unwrap();
                coords.push([first_node.lng, first_node.lat]);
                
                for edge_id in path {
                    let edge = graph.edges.get(&edge_id).unwrap();
                    let target_node = graph.nodes.get(&edge.target).unwrap();
                    coords.push([target_node.lng, target_node.lat]);
                }
            } else if start == end {
                let n = graph.nodes.get(&start).unwrap();
                coords.push([n.lng, n.lat]);
            }
            
            return Json(Some(GeoJsonFeature {
                typ: "Feature".to_string(),
                geometry: GeoJsonGeometry {
                    typ: "LineString".to_string(),
                    coordinates: coords,
                }
            }));
        }
    }
    Json(None)
}

async fn handle_isochrone(
    State(state): State<SharedState>,
    Query(req): Query<IsochroneRequest>,
) -> Json<Vec<GeoJsonFeature>> {
    let graph = state.read().unwrap();
    
    let mut features = Vec::new();
    if let Some(start) = find_nearest_node(&graph, req.lat, req.lng) {
        let edges = isochrone(&graph, start, req.time_limit_sec, req.clearance_mm);
        
        for edge_id in edges {
            let edge = graph.edges.get(&edge_id).unwrap();
            let n1 = graph.nodes.get(&edge.source).unwrap();
            let n2 = graph.nodes.get(&edge.target).unwrap();
            
            features.push(GeoJsonFeature {
                typ: "Feature".to_string(),
                geometry: GeoJsonGeometry {
                    typ: "LineString".to_string(),
                    coordinates: vec![[n1.lng, n1.lat], [n2.lng, n2.lat]],
                }
            });
        }
    }
    
    Json(features)
}
