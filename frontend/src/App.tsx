import React, { useState, useEffect } from 'react';
import { MapContainer, TileLayer, Marker, Polyline, useMapEvents } from 'react-leaflet';
import 'leaflet/dist/leaflet.css';
import L from 'leaflet';

// Fix leaflet icon issue in react
import icon from 'leaflet/dist/images/marker-icon.png';
import iconShadow from 'leaflet/dist/images/marker-shadow.png';

let DefaultIcon = L.icon({
    iconUrl: icon,
    shadowUrl: iconShadow,
    iconAnchor: [12, 41]
});
L.Marker.prototype.options.icon = DefaultIcon;

function MapEvents({ onMapClick }: { onMapClick: (e: any) => void }) {
  useMapEvents({
    click: onMapClick,
  });
  return null;
}

function App() {
  const [start, setStart] = useState<[number, number] | null>(null);
  const [end, setEnd] = useState<[number, number] | null>(null);
  const [vehicleType, setVehicleType] = useState<string>('ambulance'); // ambulance=150, rescue=500
  const [route, setRoute] = useState<[number, number][]>([]);
  const [isochrone, setIsochrone] = useState<[number, number][][]>([]);
  const [floodActive, setFloodActive] = useState(false);

  const getClearance = () => vehicleType === 'ambulance' ? 150 : 500;

  useEffect(() => {
    if (start && end) {
      fetch(`http://localhost:8080/api/route?start_lat=${start[0]}&start_lng=${start[1]}&end_lat=${end[0]}&end_lng=${end[1]}&clearance_mm=${getClearance()}`)
        .then(res => res.json())
        .then(data => {
          if (data && data.geometry && data.geometry.coordinates) {
            // GeoJSON coordinates are [lng, lat], we need [lat, lng] for Leaflet
            const latlngs = data.geometry.coordinates.map((c: any) => [c[1], c[0]]);
            setRoute(latlngs);
          } else {
             setRoute([]);
          }
        })
        .catch(err => console.error(err));
    }
  }, [start, end, vehicleType, floodActive]);

  const handleMapClick = (e: any) => {
    if (!start) {
      setStart([e.latlng.lat, e.latlng.lng]);
    } else if (!end) {
      setEnd([e.latlng.lat, e.latlng.lng]);
    } else {
      setStart([e.latlng.lat, e.latlng.lng]);
      setEnd(null);
      setRoute([]);
    }
  };

  const simulateFlood = async () => {
    if (!start) return;
    const updates = Array.from({length: 100}).map(() => ({
       lat: start[0] + (Math.random() - 0.5) * 0.01,
       lng: start[1] + (Math.random() - 0.5) * 0.01,
       depth_mm: 300
    }));
    
    await fetch('http://localhost:8080/api/flood-update', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(updates)
    });
    setFloodActive(!floodActive);
  };

  return (
    <div className="flex h-screen bg-gray-100">
      <div className="w-80 p-4 bg-white shadow-lg z-10 flex flex-col gap-4">
        <h1 className="text-2xl font-bold text-blue-600">AquaRoute</h1>
        <div className="text-sm text-gray-500">
          Real-time flood-aware routing.
        </div>

        <div className="flex flex-col gap-2">
          <label className="font-semibold">Vehicle Type</label>
          <select 
            className="p-2 border rounded"
            value={vehicleType}
            onChange={e => setVehicleType(e.target.value)}
          >
            <option value="ambulance">Standard Ambulance (150mm clearance)</option>
            <option value="rescue">Rescue Truck (500mm clearance)</option>
          </select>
        </div>

        <div className="flex flex-col gap-2">
          <button 
            className="bg-red-500 hover:bg-red-600 text-white font-bold py-2 px-4 rounded"
            onClick={simulateFlood}
          >
            Simulate Flood near Start
          </button>
          <button 
            className="bg-gray-300 hover:bg-gray-400 text-black font-bold py-2 px-4 rounded"
            onClick={() => { setStart(null); setEnd(null); setRoute([]); }}
          >
            Reset
          </button>
        </div>

        <div className="text-sm">
          <p><strong>Start:</strong> {start ? `${start[0].toFixed(4)}, ${start[1].toFixed(4)}` : 'Click on map'}</p>
          <p><strong>End:</strong> {end ? `${end[0].toFixed(4)}, ${end[1].toFixed(4)}` : 'Click on map'}</p>
        </div>
      </div>

      <div className="flex-1">
        <MapContainer center={[12.87, 74.84]} zoom={12} className="h-full w-full">
          <TileLayer
            attribution='&copy; OpenStreetMap contributors'
            url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
          />
          <MapEvents onMapClick={handleMapClick} />
          
          {start && <Marker position={start} />}
          {end && <Marker position={end} />}
          
          {route.length > 0 && <Polyline positions={route} color="blue" weight={5} />}
        </MapContainer>
      </div>
    </div>
  );
}

export default App;
