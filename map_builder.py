"""
map_builder.py — One-time script to download Mangalore's driveable street network
using a point-buffer query to bypass Nominatim administrative polygon issues.

Usage:
    python map_builder.py
"""

import osmnx as ox
import os
import time

# Center of Mangalore, India
LAT, LON = 12.87, 74.88
DIST = 5000  # 5 km radius covers the core city center and is memory-efficient

OUTPUT_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "mangalore.graphml")


def main():
    print(f"Downloading driveable street network for center ({LAT}, {LON}) with radius {DIST}m...")
    print("   This may take a minute...\n")

    start = time.time()

    # Download the driveable street network using point and radius
    G = ox.graph_from_point((LAT, LON), dist=DIST, network_type="drive")

    elapsed = time.time() - start
    print(f"Downloaded graph in {elapsed:.1f}s")
    print(f"   Nodes: {G.number_of_nodes():,}")
    print(f"   Edges: {G.number_of_edges():,}")

    # Save to .graphml for fast reload
    ox.save_graphml(G, filepath=OUTPUT_FILE)
    file_size_mb = os.path.getsize(OUTPUT_FILE) / (1024 * 1024)
    print(f"\nSaved to: {OUTPUT_FILE}")
    print(f"   File size: {file_size_mb:.1f} MB")
    print("\nReady! Start the app with: uvicorn app:app --reload")


if __name__ == "__main__":
    main()
