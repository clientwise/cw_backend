import base64
import os
from google import genai
from google.genai import types


def generate():
    client = genai.Client(
        api_key=os.environ.get("AIzaSyAoIOupDd4VBbcJMob0tTlaiGOTsP3AqXg"),
    )

    model = "gemini-2.5-flash-preview-04-17"
    contents = [
        types.Content(
            role="user",
            parts=[
                types.Part.from_text(text="""INSERT_INPUT_HERE"""),
            ],
        ),
    ]
    generate_content_config = types.GenerateContentConfig(
        temperature=0.05,
        top_p=0.5,
        response_mime_type="application/json",
        response_schema=genai.types.Schema(
                        type = genai.types.Type.OBJECT,
                        required = ["new_clients_to_add", "total_policy_pending_be_sold", "shortfall_income_this_month", "net_shortfall_till_year", "top_pros", "things_to_improve", "clients_segments_to_focus"],
                        properties = {
                            "new_clients_to_add": genai.types.Schema(
                                type = genai.types.Type.NUMBER,
                            ),
                            "total_policy_pending_be_sold": genai.types.Schema(
                                type = genai.types.Type.NUMBER,
                            ),
                            "shortfall_income_this_month": genai.types.Schema(
                                type = genai.types.Type.NUMBER,
                            ),
                            "net_shortfall_till_year": genai.types.Schema(
                                type = genai.types.Type.NUMBER,
                            ),
                            "top_pros": genai.types.Schema(
                                type = genai.types.Type.STRING,
                            ),
                            "things_to_improve": genai.types.Schema(
                                type = genai.types.Type.STRING,
                            ),
                            "clients_segments_to_focus": genai.types.Schema(
                                type = genai.types.Type.STRING,
                            ),
                        },
                    ),
    )

    for chunk in client.models.generate_content_stream(
        model=model,
        contents=contents,
        config=generate_content_config,
    ):
        print(chunk.text, end="")

if __name__ == "__main__":
    generate()
