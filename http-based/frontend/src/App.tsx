import { Routes, Route } from 'react-router-dom'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import Agents from './pages/Agents'
import Rooms from './pages/Rooms'
import RoomDetail from './pages/RoomDetail'
import Triggers from './pages/Triggers'

export default function App() {
  return (
    <Routes>
      <Route path="/" element={<Layout />}>
        <Route index element={<Dashboard />} />
        <Route path="agents" element={<Agents />} />
        <Route path="rooms" element={<Rooms />} />
        <Route path="rooms/:roomId" element={<RoomDetail />} />
        <Route path="triggers" element={<Triggers />} />
      </Route>
    </Routes>
  )
}
