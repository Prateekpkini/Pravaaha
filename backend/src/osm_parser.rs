use crate::graph::{Node, Edge, RoutingGraph, NodeId, EdgeId};
use osmpbfreader::{OsmObj, OsmPbfReader};
use std::fs::File;

pub fn parse_osm_pbf(file_path: &str) -> RoutingGraph {
    let path = std::path::Path::new(file_path);
    let r = File::open(&path).unwrap();
    let mut pbf = OsmPbfReader::new(r);
    
    let mut graph = RoutingGraph::new();
    let mut edge_id_counter: EdgeId = 0;

    let objs = pbf.get_objs_and_deps(|obj| {
        obj.is_way() && obj.tags().contains_key("highway")
    }).unwrap();

    for (_id, obj) in &objs {
        if let OsmObj::Node(node) = obj {
            graph.nodes.insert(node.id.0, Node {
                id: node.id.0,
                lat: node.lat(),
                lng: node.lon(),
                out_edges: Vec::new(),
                in_edges: Vec::new(),
            });
        }
    }

    let haversine = |lat1: f64, lon1: f64, lat2: f64, lon2: f64| -> f64 {
        let r = 6371e3; // metres
        let phi1 = lat1.to_radians();
        let phi2 = lat2.to_radians();
        let d_phi = (lat2 - lat1).to_radians();
        let d_lambda = (lon2 - lon1).to_radians();

        let a = (d_phi / 2.0).sin().powi(2)
            + phi1.cos() * phi2.cos() * (d_lambda / 2.0).sin().powi(2);
        let c = 2.0 * a.sqrt().atan2((1.0 - a).sqrt());

        r * c
    };

    for (_id, obj) in &objs {
        if let OsmObj::Way(way) = obj {
            if !way.tags.contains_key("highway") {
                continue;
            }
            
            let oneway = way.tags.get("oneway").map(|v| v.as_str()) == Some("yes");
            let speed_limit = way.tags.get("maxspeed").and_then(|v| v.parse::<f64>().ok()).unwrap_or(50.0);
            
            let mut prev_node_id = None;
            
            for &node_id in &way.nodes {
                let current_node_id = node_id.0;
                
                if let Some(prev_id) = prev_node_id {
                    if let (Some(n1), Some(n2)) = (graph.nodes.get(&prev_id), graph.nodes.get(&current_node_id)) {
                        let length = haversine(n1.lat, n1.lng, n2.lat, n2.lng);
                        let base_travel_time = length / (speed_limit * 1000.0 / 3600.0);
                        
                        let edge = Edge {
                            id: edge_id_counter,
                            source: prev_id,
                            target: current_node_id,
                            length_meters: length,
                            speed_limit_kmh: speed_limit,
                            base_travel_time,
                            current_flood_depth_mm: 0,
                        };
                        
                        let mid_lat = (n1.lat + n2.lat) / 2.0;
                        let mid_lng = (n1.lng + n2.lng) / 2.0;
                        
                        graph.edges.insert(edge.id, edge.clone());
                        graph.nodes.get_mut(&prev_id).unwrap().out_edges.push(edge.id);
                        graph.nodes.get_mut(&current_node_id).unwrap().in_edges.push(edge.id);
                        
                        graph.spatial_index.insert(rstar::primitives::PointWithData::new(edge.id, [mid_lat, mid_lng]));
                        edge_id_counter += 1;
                        
                        if !oneway {
                            let rev_edge = Edge {
                                id: edge_id_counter,
                                source: current_node_id,
                                target: prev_id,
                                length_meters: length,
                                speed_limit_kmh: speed_limit,
                                base_travel_time,
                                current_flood_depth_mm: 0,
                            };
                            graph.edges.insert(rev_edge.id, rev_edge.clone());
                            graph.nodes.get_mut(&current_node_id).unwrap().out_edges.push(rev_edge.id);
                            graph.nodes.get_mut(&prev_id).unwrap().in_edges.push(rev_edge.id);
                            
                            graph.spatial_index.insert(rstar::primitives::PointWithData::new(rev_edge.id, [mid_lat, mid_lng]));
                            edge_id_counter += 1;
                        }
                    }
                }
                prev_node_id = Some(current_node_id);
            }
        }
    }
    
    graph
}
