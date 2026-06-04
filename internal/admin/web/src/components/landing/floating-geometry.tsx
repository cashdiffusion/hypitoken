import { useRef } from "react";
import { Canvas, useFrame, type RootState } from "@react-three/fiber";
import * as THREE from "three";

// Slow-tumbling wireframe icosahedron — ambient "floating geometry" for a
// section backdrop. Wireframe + low opacity keeps it as atmosphere, never a
// focal element. Drifts gently toward the pointer.
function Shape({ color }: { color: string }) {
  const group = useRef<THREE.Group>(null);
  const mesh = useRef<THREE.Mesh>(null);

  useFrame((state: RootState, delta: number) => {
    if (mesh.current) {
      mesh.current.rotation.x += delta * 0.12;
      mesh.current.rotation.y += delta * 0.16;
    }
    if (group.current) {
      const tx = state.pointer.x * 0.4;
      const ty = state.pointer.y * 0.3;
      group.current.position.x += (tx - group.current.position.x) * 0.04;
      group.current.position.y += (ty - group.current.position.y) * 0.04;
    }
  });

  return (
    <group ref={group}>
      <mesh ref={mesh}>
        <icosahedronGeometry args={[1.6, 1]} />
        <meshBasicMaterial color={color} wireframe transparent opacity={0.22} />
      </mesh>
      {/* faint solid core for a touch of volume */}
      <mesh scale={0.62}>
        <icosahedronGeometry args={[1.6, 0]} />
        <meshBasicMaterial color={color} transparent opacity={0.05} />
      </mesh>
    </group>
  );
}

export default function FloatingGeometry({ color = "#34d399" }: { color?: string }) {
  return (
    <Canvas
      camera={{ position: [0, 0, 4.2], fov: 55 }}
      dpr={[1, 1.5]}
      gl={{ antialias: true, alpha: true, powerPreference: "low-power" }}
      style={{ position: "absolute", inset: 0 }}
    >
      <Shape color={color} />
    </Canvas>
  );
}
