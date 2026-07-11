use crate::graph::{EdgeId, NodeId, RoutingGraph};
use std::collections::{BinaryHeap, HashMap};
use std::cmp::Ordering;

#[derive(Copy, Clone, PartialEq)]
struct State {
    cost: f64,
    node: NodeId,
}

impl Eq for State {}

impl Ord for State {
    fn cmp(&self, other: &Self) -> Ordering {
        other.cost.partial_cmp(&self.cost).unwrap_or(Ordering::Equal)
    }
}

impl PartialOrd for State {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

fn haversine(lat1: f64, lon1: f64, lat2: f64, lon2: f64) -> f64 {
    let r = 6371e3; // metres
    let phi1 = lat1.to_radians();
    let phi2 = lat2.to_radians();
    let d_phi = (lat2 - lat1).to_radians();
    let d_lambda = (lon2 - lon1).to_radians();

    let a = (d_phi / 2.0).sin().powi(2)
        + phi1.cos() * phi2.cos() * (d_lambda / 2.0).sin().powi(2);
    let c = 2.0 * a.sqrt().atan2((1.0 - a).sqrt());
    r * c
}

pub fn bidirectional_astar(
    graph: &RoutingGraph,
    start: NodeId,
    goal: NodeId,
    max_vehicle_clearance_mm: u32,
) -> Option<Vec<EdgeId>> {
    if start == goal {
        return Some(Vec::new());
    }
    
    let mut fwd_open = BinaryHeap::new();
    let mut bwd_open = BinaryHeap::new();
    
    let mut fwd_g = HashMap::new();
    let mut bwd_g = HashMap::new();
    
    let mut fwd_parent: HashMap<NodeId, EdgeId> = HashMap::new();
    let mut bwd_parent: HashMap<NodeId, EdgeId> = HashMap::new();
    
    fwd_g.insert(start, 0.0);
    bwd_g.insert(goal, 0.0);
    
    fwd_open.push(State { cost: 0.0, node: start });
    bwd_open.push(State { cost: 0.0, node: goal });
    
    let mut mu = f64::INFINITY;
    let mut meeting_node = None;
    
    let target_node = graph.nodes.get(&goal)?;
    let start_node = graph.nodes.get(&start)?;

    while !fwd_open.is_empty() && !bwd_open.is_empty() {
        let fwd_top = fwd_open.peek().unwrap().cost;
        let bwd_top = bwd_open.peek().unwrap().cost;
        
        if fwd_top + bwd_top >= mu {
            break;
        }
        
        if let Some(State { cost: _, node: u }) = fwd_open.pop() {
            let current_fwd_g = *fwd_g.get(&u).unwrap_or(&f64::INFINITY);
            
            if let Some(node_u) = graph.nodes.get(&u) {
                for &edge_id in &node_u.out_edges {
                    if let Some(edge) = graph.edges.get(&edge_id) {
                        if edge.current_flood_depth_mm > max_vehicle_clearance_mm {
                            continue;
                        }
                        let v = edge.target;
                        let new_g = current_fwd_g + edge.base_travel_time;
                        
                        if new_g < *fwd_g.get(&v).unwrap_or(&f64::INFINITY) {
                            fwd_g.insert(v, new_g);
                            fwd_parent.insert(v, edge_id);
                            
                            let node_v = graph.nodes.get(&v).unwrap();
                            let h = haversine(node_v.lat, node_v.lng, target_node.lat, target_node.lng);
                            let h_time = h / (130.0 * 1000.0 / 3600.0);
                            
                            fwd_open.push(State { cost: new_g + h_time, node: v });
                            
                            if let Some(b_g) = bwd_g.get(&v) {
                                if new_g + b_g < mu {
                                    mu = new_g + b_g;
                                    meeting_node = Some(v);
                                }
                            }
                        }
                    }
                }
            }
        }
        
        if let Some(State { cost: _, node: u }) = bwd_open.pop() {
            let current_bwd_g = *bwd_g.get(&u).unwrap_or(&f64::INFINITY);
            
            if let Some(node_u) = graph.nodes.get(&u) {
                for &edge_id in &node_u.in_edges {
                    if let Some(edge) = graph.edges.get(&edge_id) {
                        if edge.current_flood_depth_mm > max_vehicle_clearance_mm {
                            continue;
                        }
                        let v = edge.source;
                        let new_g = current_bwd_g + edge.base_travel_time;
                        
                        if new_g < *bwd_g.get(&v).unwrap_or(&f64::INFINITY) {
                            bwd_g.insert(v, new_g);
                            bwd_parent.insert(v, edge_id);
                            
                            let node_v = graph.nodes.get(&v).unwrap();
                            let h = haversine(node_v.lat, node_v.lng, start_node.lat, start_node.lng);
                            let h_time = h / (130.0 * 1000.0 / 3600.0);
                            
                            bwd_open.push(State { cost: new_g + h_time, node: v });
                            
                            if let Some(f_g) = fwd_g.get(&v) {
                                if new_g + f_g < mu {
                                    mu = new_g + f_g;
                                    meeting_node = Some(v);
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    
    if let Some(meet) = meeting_node {
        let mut path = Vec::new();
        let mut current = meet;
        while let Some(&edge_id) = fwd_parent.get(&current) {
            path.push(edge_id);
            current = graph.edges.get(&edge_id).unwrap().source;
        }
        path.reverse();
        
        let mut current = meet;
        while let Some(&edge_id) = bwd_parent.get(&current) {
            path.push(edge_id);
            current = graph.edges.get(&edge_id).unwrap().target;
        }
        
        return Some(path);
    }
    
    None
}

pub fn isochrone(
    graph: &RoutingGraph,
    start: NodeId,
    time_limit_seconds: f64,
    max_vehicle_clearance_mm: u32,
) -> Vec<EdgeId> {
    let mut open = BinaryHeap::new();
    let mut g = HashMap::new();
    let mut reachable_edges = Vec::new();
    
    g.insert(start, 0.0);
    open.push(State { cost: 0.0, node: start });
    
    while let Some(State { cost, node: u }) = open.pop() {
        if cost > time_limit_seconds {
            continue;
        }
        
        if cost > *g.get(&u).unwrap_or(&f64::INFINITY) {
            continue;
        }
        
        if let Some(node_u) = graph.nodes.get(&u) {
            for &edge_id in &node_u.out_edges {
                if let Some(edge) = graph.edges.get(&edge_id) {
                    if edge.current_flood_depth_mm > max_vehicle_clearance_mm {
                        continue;
                    }
                    
                    reachable_edges.push(edge_id);
                    let v = edge.target;
                    let new_g = cost + edge.base_travel_time;
                    
                    if new_g <= time_limit_seconds && new_g < *g.get(&v).unwrap_or(&f64::INFINITY) {
                        g.insert(v, new_g);
                        open.push(State { cost: new_g, node: v });
                    }
                }
            }
        }
    }
    
    reachable_edges.sort_unstable();
    reachable_edges.dedup();
    reachable_edges
}
