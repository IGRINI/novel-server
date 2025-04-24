from diffusers import DiffusionPipeline

pipeline = DiffusionPipeline.from_pretrained("Efficient-Large-Model/Sana_Sprint_1.6B_1024px_diffusers")
pipeline.save_pretrained("./sana-local")