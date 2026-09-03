import yaml

class PipelineEngine:
    def __init__(self, registry, profiles_dir):
        self.registry = registry
        self.profiles_dir = profiles_dir

    def load_profile(self, profile_name):
        profile_path = f"{self.profiles_dir}/{profile_name}.yaml"
        try:
            with open(profile_path, 'r') as f:
                return yaml.safe_load(f)
        except Exception as e:
            raise RuntimeError(f"Failed to load profile {profile_name}: {e}")

    def execute_profile(self, profile_name, event_context):
        profile = self.load_profile(profile_name)
        print(f"Executing profile: {profile.get('name', profile_name)}")
        
        # 简单模拟执行过程
        for stage in profile.get('stages', []):
            print(f"\n--- Stage: {stage['name']} ---")
            for step in stage.get('steps', []):
                self._execute_step(step, event_context)

    def _execute_step(self, step, event_context):
        if 'plugin_id' in step:
            plugin = self.registry.get_plugin_by_id(step['plugin_id'])
            if plugin:
                print(f"  -> Running plugin: {plugin['id']}")
            else:
                print(f"  -> Error: Plugin {step['plugin_id']} not found.")
                
        elif 'capability' in step:
            # 假设从 event_context 中提取目标语言
            target_langs = event_context.get('workspace_context', {}).get('target_languages', [])
            lang = target_langs[0] if target_langs else None
            
            plugins = self.registry.find_plugins_by_capability(step['capability'], lang)
            if plugins:
                for p in plugins:
                    print(f"  -> Running mapped plugin (by capability): {p['id']}")
            else:
                print(f"  -> Error: No plugin found for capability {step['capability']} (lang: {lang})")
