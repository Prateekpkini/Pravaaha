use std::sync::RwLock;
use std::collections::HashMap;

pub type NodeId = i64;
pub type EdgeId = i64;

#[derive(Debug, Clone)]
pub struct Node {
    pub id: NodeId,
    pub lat: f64,
    pub lng: f64,
    pub out_edges: Vec<EdgeId>,
    pub in_edges: Vec<EdgeId>,
}

#[derive(Debug, Clone)]
pub struct Edge {
    pub id: EdgeId,
    pub source: NodeId,
    pub target: NodeId,
    pub length_meters: f64,
    pub speed_limit_kmh: f64,
    pub base_travel_time: f64,
    pub current_flood_depth_mm: u32,
}

pub struct RoutingGraph {
    pub nodes: HashMap<NodeId, Node>,
    pub edges: HashMap<EdgeId, Edge>,
    pub spatial_index: rstar::RTree<rstar::primitives::PointWithData<EdgeId, [f64; 2]>>,
}

impl RoutingGraph {
    pub fn new() -> Self {
        Self {
            nodes: HashMap::new(),
            edges: HashMap::new(),
            spatial_index: rstar::RTree::new(),
        }
    }
}
