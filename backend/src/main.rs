mod api;
mod graph;
mod osm_parser;
mod routing;

use std::sync::{Arc, RwLock};
use tower_http::cors::CorsLayer;

#[tokio::main]
async fn main() {
    println!("Parsing OSM PBF file...");
    let graph = osm_parser::parse_osm_pbf("../monaco-latest.osm.pbf");
    println!("Graph loaded: {} nodes, {} edges", graph.nodes.len(), graph.edges.len());

    let shared_state = Arc::new(RwLock::new(graph));
    
    let cors = CorsLayer::permissive();

    let app = api::create_router(shared_state).layer(cors);

    let listener = tokio::net::TcpListener::bind("0.0.0.0:8080").await.unwrap();
    println!("Server running on http://localhost:8080");
    axum::serve(listener, app).await.unwrap();
}
