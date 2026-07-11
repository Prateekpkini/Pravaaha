# 🌊 Pravaaha - Flood-Aware Evacuation Routing Engine

**Coastal Innovation Hackathon 2026**

> Smart routing for safer evacuations during flood emergencies

---

## 📋 Project Overview

**Pravaaha** is an intelligent evacuation routing system designed to help people find the safest routes during flood emergencies. By analyzing real-time flood simulations and dynamically adjusting road weights, the system provides optimal evacuation routes that avoid waterlogged areas, enabling faster and safer emergency responses in coastal cities.

This project was developed for the **Coastal Innovation Hackathon** and focuses on enhancing disaster management and public safety in flood-prone regions like Mangalore.

---

## 👥 Team Technova

| Member | Role |
|--------|------|
| **KS Shravya** | Development |
| **Prateek** | Development |
| **Disha P V** | Development |
| **Charanya Gowda** | Development |

---

## ✨ Key Features

- **Real-Time Flood Simulation**: Synthetically model flood scenarios on road networks
- **Dynamic Route Optimization**: Routes automatically adapt when roads become flooded
- **Sub-Second Pathfinding**: Fast Dijkstra-based routing on large street networks
- **RESTful API**: Easy integration with frontend applications
- **Interactive Web Interface**: Visualize routes and flooded areas on an interactive map
- **Scalable Design**: Efficiently handles street networks with thousands of nodes and edges

---

## 🛠️ Tech Stack

- **Backend**: FastAPI, Uvicorn
- **Graph Processing**: NetworkX, OSMnx
- **Spatial Analysis**: SciPy, scikit-learn
- **Frontend**: HTML5, CSS3, JavaScript
- **Data Source**: OpenStreetMap (via OSMnx)

---

## 📦 Installation

### Prerequisites
- Python 3.11+
- pip package manager

### Setup Instructions

1. **Clone or navigate to the project directory**
   ```bash
   cd Pravaaha
   ```

2. **Create and activate a virtual environment** (optional but recommended)
   ```bash
   python -m venv .venv
   .venv\Scripts\activate  # On Windows
   ```

3. **Install dependencies**
   ```bash
   pip install -r requirements.txt
   ```

   Required packages:
   - fastapi
   - uvicorn[standard]
   - osmnx
   - networkx
   - scipy
   - scikit-learn

---

## 🚀 Running the Application

1. **Start the FastAPI server**
   ```bash
   uvicorn app:app --reload
   ```

2. **Access the application**
   - Web Interface: http://127.0.0.1:8000
   - API Docs (Swagger UI): http://127.0.0.1:8000/docs
   - Alternative Docs (ReDoc): http://127.0.0.1:8000/redoc

The server will load the Mangalore street graph into memory at startup (this may take a few seconds depending on graph size).

---

## 📡 API Endpoints

### 1. **Health Check**
```
GET /health
```
Returns server status and graph statistics.

**Response:**
```json
{
  "status": "ok",
  "graph": {
    "nodes": 12345,
    "edges": 67890
  },
  "flood_active": false,
  "flooded_road_count": 0
}
```

### 2. **Get Route**
```
GET /get-route?start_lat=12.9716&start_lon=74.7936&end_lat=12.9352&end_lon=74.8668
```
Calculates the shortest driveable route between two coordinates, respecting flood weights.

**Parameters:**
- `start_lat` (float): Starting latitude
- `start_lon` (float): Starting longitude
- `end_lat` (float): Destination latitude
- `end_lon` (float): Destination longitude

**Response:**
```json
{
  "route": [
    [12.9716, 74.7936],
    [12.9720, 74.7945],
    [12.9352, 74.8668]
  ],
  "distance_m": 15234.5,
  "distance_km": 15.23,
  "node_count": 145,
  "flood_active": false
}
```

### 3. **Simulate Flood**
```
POST /simulate-flood
```
Randomly selects 5-10 road segments and increases their traversal weight by 1000x to simulate flooding.

**Response:**
```json
{
  "flooded_roads": 7,
  "geometries": [
    [[12.9716, 74.7936], [12.9720, 74.7945]],
    ...
  ],
  "total_flooded_segments": 7
}
```

---

## 📁 Project Structure

```
Pravaaha/
├── app.py                 # FastAPI application and API endpoints
├── map_builder.py         # Script to download and build the street graph
├── mangalore.graphml      # Pre-built street graph for Mangalore
├── requirements.txt       # Python dependencies
├── README.md             # This file
├── static/
│   ├── index.html        # Web interface
│   ├── app.js            # Frontend logic
│   └── style.css         # Styling
└── cache/                # Cached graph data files
```

---

## 🔄 How It Works

1. **Graph Loading**: On startup, the application loads the Mangalore street network from `mangalore.graphml` into memory
2. **Flood Simulation**: Users can trigger `/simulate-flood` to randomly flood road segments
3. **Dynamic Weighting**: Flooded edges get a 1000x weight multiplier, making them less preferable for routing
4. **Route Calculation**: Dijkstra's algorithm finds the optimal path considering current flood conditions
5. **Frontend Visualization**: The web interface displays routes and flooded areas on an interactive map

---

## 💡 Use Cases

- **Emergency Services**: Dispatchers can quickly find optimal evacuation routes
- **Real-Time Navigation**: Citizens get accurate routing during actual flood events
- **Planning & Preparation**: Authorities can simulate scenarios and plan evacuation strategies
- **Infrastructure Assessment**: Identify critical roads for reinforcement or elevation

---

## 🔧 Development & Customization

### Adding a New City
1. Update `GRAPH_PATH` in `app.py` to point to your city's GraphML file
2. Run `map_builder.py` with your desired city coordinates to download OSM data
3. Restart the server

### Adjusting Flood Parameters
Modify the flood weight multiplier in `/simulate-flood` endpoint:
```python
# Current: 1000x multiplier
# Edit this value for different severity levels
```

### Performance Tuning
- Adjust graph caching in `map_builder.py`
- Optimize edge filtering in `get_route` endpoint
- Consider spatial indexing for larger networks

---

## 📊 Performance Metrics

- **Route Calculation**: Sub-second for typical queries
- **Graph Size**: ~12K nodes, ~68K edges for Mangalore
- **API Response Time**: <500ms average

---

## 🚨 Error Handling

The API provides meaningful error responses:

| Status Code | Meaning |
|------------|---------|
| 200 | Success |
| 400 | Invalid coordinates or same start/end point |
| 404 | No route found between points |
| 503 | Graph not loaded or service unavailable |

---

## 📝 Notes

- The application uses synthetic flood simulation for testing purposes
- Real-world integration would require live flood data from weather APIs or IoT sensors
- The graph is loaded entirely into memory for performance; for very large networks, consider distributed solutions

---

## 🎯 Future Enhancements

- [ ] Integration with real-time weather APIs for actual flood predictions
- [ ] Multi-modal routing (walking, public transport alternatives)
- [ ] Historical flood data analysis
- [ ] Traffic congestion integration
- [ ] Mobile app with offline capability
- [ ] Accessibility routing (wheelchair-friendly paths)
- [ ] Real-time collaborative visualization for emergency coordinators

---

## 📄 License

This project was developed for the Coastal Innovation Hackathon 2026.

---

## 📞 Support

For questions or issues, please refer to the API documentation at `/docs` when the server is running.

---

**Developed with ❤️ by Team Technova**  
*Coastal Innovation Hackathon 2026*
