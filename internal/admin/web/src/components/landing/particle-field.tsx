import { Canvas, type RootState, useFrame } from "@react-three/fiber";
import { useMemo, useRef } from "react";
import * as THREE from "three";
import { useAmbientFrameloop } from "./use-ambient-frameloop";
import { WebGLGuard } from "./webgl-guard";

// A slow-drifting shell of green points — ambient depth behind the hero,
// not a focal element. Reacts gently to the pointer for parallax. Kept cheap:
// no lights, additive blending, depthWrite off, capped DPR.
function Points({ count, color }: { count: number; color: string }) {
  const ref = useRef<THREE.Points>(null);

  const positions = useMemo(() => {
    const arr = new Float32Array(count * 3);
    for (let i = 0; i < count; i++) {
      const r = 3.5 + Math.random() ** 0.6 * 7;
      const theta = Math.random() * Math.PI * 2;
      const phi = Math.acos(2 * Math.random() - 1);
      arr[i * 3] = r * Math.sin(phi) * Math.cos(theta);
      arr[i * 3 + 1] = r * Math.sin(phi) * Math.sin(theta) * 0.6;
      arr[i * 3 + 2] = r * Math.cos(phi);
    }
    return arr;
  }, [count]);

  useFrame((state: RootState, delta: number) => {
    const p = ref.current;
    if (!p) return;
    p.rotation.y += delta * 0.025;
    // ease the whole field toward the pointer for a subtle parallax tilt
    const tx = state.pointer.x * 0.12;
    const ty = state.pointer.y * 0.08;
    p.rotation.x += (ty - p.rotation.x) * 0.03;
    p.position.x += (tx - p.position.x) * 0.03;
  });

  return (
    <points ref={ref}>
      <bufferGeometry>
        <bufferAttribute attach="attributes-position" args={[positions, 3]} />
      </bufferGeometry>
      <pointsMaterial
        size={0.05}
        color={color}
        transparent
        opacity={0.85}
        sizeAttenuation
        depthWrite={false}
        blending={THREE.AdditiveBlending}
      />
    </points>
  );
}

export default function ParticleField({
  color = "#34d399",
  count = 2600,
}: {
  color?: string;
  count?: number;
}) {
  const { ref, frameloop } = useAmbientFrameloop();

  return (
    <div ref={ref} style={{ position: "absolute", inset: 0 }}>
      <WebGLGuard>
        <Canvas
          frameloop={frameloop}
          camera={{ position: [0, 0, 9], fov: 60 }}
          dpr={[1, 1.5]}
          gl={{ antialias: false, alpha: true, powerPreference: "low-power" }}
          style={{ position: "absolute", inset: 0 }}
        >
          <Points count={count} color={color} />
        </Canvas>
      </WebGLGuard>
    </div>
  );
}
