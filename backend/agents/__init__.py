r"""agents\: the registry and executor for AgentConfig (B9).

managers\agent.py holds the data model (AgentConfig), same as
managers\session.py holds Session and managers\project.py holds
Project. This package holds the business rules on top of it -- built-in
seeding and resolution (registry.py), and the one execution path every
message goes through (executor.py) -- the same split ProjectManager /
ProjectRepository / managers\project.py already uses.
"""
