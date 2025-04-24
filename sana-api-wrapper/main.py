import io
import logging
import os
import subprocess
import tempfile
import time
from contextlib import asynccontextmanager

import torch
from diffusers import DiffusionPipeline
from dotenv import load_dotenv
from fastapi import FastAPI, HTTPException
from fastapi.responses import Response
from PIL import Image
from pydantic import BaseModel

# --- Configuration ---
load_dotenv()
os.environ["PATH"] += os.pathsep + "/opt/mozjpeg/bin"  # Ensure cjpeg is in PATH
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

ml_models = {}

@asynccontextmanager
async def lifetime(app: FastAPI):
    device = "cuda" if torch.cuda.is_available() else "cpu"
    model_path = os.getenv("SANA_SPRINT_MODEL_PATH", "./sana-local")
    logger.info(f"Loading SANA Sprint model from: {model_path} onto device: {device}")

    try:
        pipeline = DiffusionPipeline.from_pretrained(
            model_path,
            torch_dtype=torch.bfloat16
        )
        pipeline.to(device)

        ml_models["sana_sprint"] = pipeline
        ml_models["device"] = device
        logger.info("SANA Sprint model loaded successfully.")
        yield

    except Exception as e:
        logger.error(f"Failed to load SANA Sprint model: {e}", exc_info=True)
        raise RuntimeError(f"Failed to load model: {e}")
    finally:
        logger.info("Cleaning up SANA Sprint model...")
        if "sana_sprint" in ml_models:
            del ml_models["sana_sprint"]
        if torch.cuda.is_available():
            torch.cuda.empty_cache()
        logger.info("Model cleaned up.")

app = FastAPI(lifespan=lifetime)

class GenerationRequest(BaseModel):
    prompt: str
    seed: int | None = None

def generate_image(pipeline, device, prompt: str, seed: int | None = None) -> Image.Image:
    logger.info(f"Generating image for prompt: '{prompt[:50]}...'")
    start_time = time.time()

    generator = torch.Generator(device=device).manual_seed(seed) if seed else None

    image = pipeline(
        prompt=prompt,
        num_inference_steps=2,
        generator=generator
    ).images[0]

    # Crop to 2:3 center
    width, height = image.size
    target_ratio = 2 / 3
    target_width = width
    target_height = int(width / target_ratio)

    if target_height > height:
        target_height = height
        target_width = int(height * target_ratio)

    left = (width - target_width) // 2
    top = (height - target_height) // 2
    right = left + target_width
    bottom = top + target_height
    image = image.crop((left, top, right, bottom))

    end_time = time.time()
    logger.info(f"Image generated in {end_time - start_time:.2f} seconds")
    return image

@app.get("/health")
async def health_check():
    return {"status": "ok", "model_loaded": "sana_sprint" in ml_models}

@app.post("/generate")
async def api_generate_image(request: GenerationRequest):
    pipeline = ml_models.get("sana_sprint")
    device = ml_models.get("device")

    if not pipeline:
        logger.error("Model not loaded, cannot generate image.")
        raise HTTPException(status_code=500, detail="Image generation model not available")

    try:
        pil_image = generate_image(
            pipeline,
            device,
            request.prompt,
            request.seed
        )

        # Save to temporary BMP file
        with tempfile.NamedTemporaryFile(delete=False, suffix=".bmp") as tmp_bmp:
            pil_image.save(tmp_bmp.name, format="BMP")

            # Convert to mozjpeg
            with tempfile.NamedTemporaryFile(delete=False, suffix=".jpg") as tmp_jpg:
                subprocess.run([
                    "cjpeg", "-quality", "85", "-progressive",
                    "-outfile", tmp_jpg.name, tmp_bmp.name
                ], check=True)
                with open(tmp_jpg.name, "rb") as f:
                    img_data = f.read()

        return Response(content=img_data, media_type="image/jpeg")

    except Exception as e:
        logger.error(f"Error during image generation: {e}", exc_info=True)
        raise HTTPException(status_code=500, detail=f"Image generation failed: {e}")

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
